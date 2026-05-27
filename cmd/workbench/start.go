package workbench

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var (
		cores       int
		memMB       int
		idleHours   int
		ide         string
		projectDir  string
		noTelemetry bool
		nodeName    string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start an interactive workbench session",
		Long: `Start a Nomad service job running code-server with your homedir mounted.

The session is accessible from your browser over Tailscale. The IDE URL
and password are printed after the session is ready. Use 'abc workbench
stop' to release resources when done.

The homedir (/home/<user>/ inside the session) is persisted on the cluster
node across session restarts. Your data and installed packages survive
'abc workbench stop' + 'abc workbench start'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, cores, memMB, idleHours, ide, projectDir, noTelemetry, nodeName)
		},
	}

	cmd.Flags().IntVar(&cores, "cores", 2, "CPU cores")
	cmd.Flags().IntVar(&memMB, "mem", 4096, "Memory in MB")
	cmd.Flags().IntVar(&idleHours, "idle-hours", 4, "Idle timeout in hours (0 = no timeout)")
	cmd.Flags().StringVar(&ide, "ide", "quarto", "IDE: quarto or code-server (positron: not yet implemented)")
	cmd.Flags().StringVar(&projectDir, "project", "", "Open this directory in the IDE")
	cmd.Flags().BoolVar(&noTelemetry, "no-telemetry", false, "Disable session telemetry sidecar")
	cmd.Flags().StringVar(&nodeName, "node", "", "Pin session to this Nomad node (default: platform node from context)")

	return cmd
}

func runStart(cmd *cobra.Command, cores, memMB, idleHours int, ide, projectDir string, noTelemetry bool, nodeName string) error {
	if ide == "positron" {
		return fmt.Errorf("positron Remote SSH is not yet implemented at seedling")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	// Resolve user from context (whoami).
	user := strings.TrimSpace(ctx.Admin.Whoami)
	if user == "" && ctx.Auth != nil {
		user = strings.TrimSpace(ctx.Auth.Whoami)
	}
	if user == "" {
		return fmt.Errorf("cannot determine user: run 'abc auth whoami' or ensure active context has admin.whoami set")
	}

	// Open local DB and check for existing session.
	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local db: %w", err)
	}

	existing, err := workbench.ActiveSession(context.Background(), db, user)
	if err != nil && !errors.Is(err, workbench.ErrNoSession) {
		return fmt.Errorf("check existing session: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("a session is already running (id=%s)\nuse 'abc workbench stop' first, or 'abc workbench status' to see it", existing.SessionID)
	}

	// Check for prior sessions (for messaging).
	_, priorErr := workbench.LatestSession(context.Background(), db, user)
	isFirstSession := errors.Is(priorErr, workbench.ErrNoSession)

	// Resolve Nomad namespace — user's own namespace (e.g. su-mbhg-hostgen for pool
	// users, "default" for admin contexts). Workbench jobs live alongside the user's
	// pipeline runs, so their token already has the required permissions.
	ns := ctx.NomadNamespace()
	if ns == "" {
		ns = "default"
	}

	// Build Nomad client.
	nc := utils.NomadClientFromConfig().WithNamespace(ns)

	// Resolve node name — from flag, or look up the platform node.
	if nodeName == "" {
		nodeName, err = resolvePlatformNode(nc, ctx)
		if err != nil {
			return fmt.Errorf("resolve workbench node: %w\nuse --node <node-name> to specify explicitly", err)
		}
	}

	// Resolve MinIO credentials from context.
	s3Endpoint, s3AccessKey, s3SecretKey := resolveS3Creds(ctx)

	// Generate session ID and token.
	sessionID := state.NewWorkbenchID()
	token := randomToken(16)

	// Determine datacenter.
	datacenter := "seedling-prod"
	if ctx.Admin.ABCNodes != nil {
		// abc-nodes cluster — derive from context if available
	}

	idleTimeoutSecs := idleHours * 3600
	if idleHours == 0 {
		idleTimeoutSecs = 0
	}

	// Generate HCL.
	jcfg := workbench.JobConfig{
		User:            user,
		SessionID:       sessionID,
		Token:           token,
		Cores:           cores,
		MemoryMB:        memMB,
		Namespace:       ns,
		Datacenter:      datacenter,
		NodeName:        nodeName,
		IDE:             ide,
		ProjectDir:      projectDir,
		Telemetry:       !noTelemetry,
		IdleTimeoutSecs: idleTimeoutSecs,
		S3Endpoint:      s3Endpoint,
		S3AccessKey:     s3AccessKey,
		S3SecretKey:     s3SecretKey,
	}
	hcl := workbench.GenerateHCL(jcfg)

	// Parse + register.
	fmt.Fprintf(cmd.ErrOrStderr(), "Submitting workbench job...\n")
	jobJSON, err := nc.ParseHCL(context.Background(), hcl)
	if err != nil {
		return fmt.Errorf("parse HCL: %w", err)
	}
	reg, err := nc.RegisterJob(context.Background(), jobJSON)
	if err != nil {
		return fmt.Errorf("register job: %w", err)
	}

	jobID := "abc-workbench-" + user

	// Insert session row as starting.
	sess := &workbench.Session{
		SessionID:       sessionID,
		User:            user,
		JobID:           jobID,
		Namespace:       ns,
		IDE:             ide,
		Cores:           cores,
		MemoryMB:        memMB,
		GPUs:            0,
		Telemetry:       !noTelemetry,
		IdleTimeoutSecs: idleTimeoutSecs,
		Token:           token,
		Status:          "starting",
	}
	if err := workbench.InsertSession(context.Background(), db, sess); err != nil {
		return fmt.Errorf("record session: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Job registered (eval: %s, ns: %s). Waiting for allocation...\n", reg.EvalID[:8], ns)

	// Poll for running alloc and dynamic port.
	host, port, allocID, pollErr := pollRunning(nc, jobID, ns, 90*time.Second)
	if pollErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\nSession submitted but not yet running. Try 'abc workbench status'.\n", pollErr)
		return nil
	}

	// Update DB with host:port.
	if err := workbench.UpdateRunning(context.Background(), db, sessionID, host, port); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update session record: %v\n", err)
	}

	_ = allocID // available for future log attachment

	ideURL := fmt.Sprintf("http://%s:%d", host, port)

	if isFirstSession {
		fmt.Printf(`Your workbench session is ready.

  IDE:      %s  (password: abc workbench url)
  Homedir:  /home/%s/            (persists across sessions on this node)
  MinIO:    s3://%s/             (pipeline outputs, uploaded data)

Note: your homedir and MinIO storage are separate. Use s5cmd or mc inside
the terminal to move data between them. Run 'abc workbench url' to show
credentials and the IDE link again.
`, ideURL, user, user)
	} else {
		fmt.Printf("Session ready: %s\n  password: %s\n", ideURL, token)
	}
	return nil
}

// pollRunning waits for the workbench job to have a running alloc and returns
// the host IP, dynamic port, and alloc ID.
func pollRunning(nc *utils.NomadClient, jobID, namespace string, timeout time.Duration) (host string, port int, allocID string, err error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allocs, listErr := nc.GetJobAllocs(context.Background(), jobID, namespace, false)
		if listErr != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		for _, a := range allocs {
			if a.ClientStatus != "running" {
				continue
			}
			full, getErr := nc.GetAllocation(context.Background(), a.ID, namespace)
			if getErr != nil || full == nil {
				continue
			}
			if full.AllocatedResources == nil || full.AllocatedResources.Shared == nil {
				continue
			}
			for _, net := range full.AllocatedResources.Shared.Networks {
				for _, dp := range net.DynamicPorts {
					if dp.Label == "http" && dp.Value > 0 {
						// In bridge mode, DynamicPorts[].HostIP may be empty;
						// fall back to the network-level IP (host-side bind address).
						hostIP := dp.HostIP
						if hostIP == "" {
							hostIP = net.IP
						}
						return hostIP, dp.Value, a.ID, nil
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	return "", 0, "", fmt.Errorf("timed out waiting for workbench session to start (checked for %s)", timeout)
}

// resolvePlatformNode looks up the Nomad node with node_pool=platform and
// returns its unique name. Falls back to "aither" for seedling.
func resolvePlatformNode(nc *utils.NomadClient, ctx config.Context) (string, error) {
	// For seedling (single platform node), use the well-known name.
	// A fuller implementation would query /v1/nodes?filter=NodePool==platform.
	// For now, if config has a workbench node configured, use that.
	if wn, ok := config.GetAdminFloorField(&ctx.Admin.Services, "workbench", "node"); ok && wn != "" {
		return wn, nil
	}
	// Seedling default.
	return "aither", nil
}

// resolveS3Creds extracts S3 credentials from the active context.
func resolveS3Creds(ctx config.Context) (endpoint, accessKey, secretKey string) {
	for _, svc := range []string{"minio", "rustfs"} {
		ep, ok := config.GetAdminFloorField(&ctx.Admin.Services, svc, "endpoint")
		if !ok || ep == "" {
			ep, ok = config.GetAdminFloorField(&ctx.Admin.Services, svc, "http")
			if !ok || ep == "" {
				continue
			}
		}
		ak, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "access_key")
		if ak == "" {
			ak, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "user")
		}
		sk, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "secret_key")
		if sk == "" {
			sk, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "password")
		}
		if ep != "" && ak != "" {
			return ep, ak, sk
		}
	}
	// abc-nodes fallback.
	if ctx.Admin.ABCNodes != nil {
		n := ctx.Admin.ABCNodes
		if n.S3AccessKey != "" {
			ep, _ := config.GetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint")
			return ep, n.S3AccessKey, n.S3SecretKey
		}
	}
	return "", "", ""
}

// randomToken generates a random hex-encoded string of tokenLen characters.
func randomToken(tokenLen int) string {
	b := make([]byte, tokenLen)
	if _, err := rand.Read(b); err != nil {
		// Should never fail; fallback to a fixed pattern so the session still works.
		return strings.Repeat("x", tokenLen*2)[:tokenLen]
	}
	return hex.EncodeToString(b)[:tokenLen]
}
