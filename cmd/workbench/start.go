package workbench

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
		backend     string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start an interactive workbench session",
		Long: `Start a workbench session running code-server and Jupyter with your homedir mounted.

The session is accessible from your browser and from VS Code / Positron via
Remote SSH. The IDE URL and SSH alias are printed after the session is ready.
Use 'abc workbench stop' to release resources when done.

The homedir (/home/ubuntu/ inside the session) is persisted on the cluster
node across session restarts. Your data and installed packages survive
'abc workbench stop' + 'abc workbench start'.

Backends:
  docker  — Nomad service job running a container (default, available immediately)
  vm      — Multipass VM per user on the platform node (full Linux env, SSH via ProxyJump)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if backend == "vm" {
				return runStartVM(cmd, cores, memMB, idleHours, ide, projectDir, noTelemetry, nodeName)
			}
			return runStart(cmd, cores, memMB, idleHours, ide, projectDir, noTelemetry, nodeName)
		},
	}

	cmd.Flags().IntVar(&cores, "cores", 2, "CPU cores")
	cmd.Flags().IntVar(&memMB, "mem", 4096, "Memory in MB")
	cmd.Flags().IntVar(&idleHours, "idle-hours", 4, "Idle timeout in hours (0 = no timeout)")
	cmd.Flags().StringVar(&ide, "ide", "quarto", "IDE: quarto or code-server (positron: not yet implemented)")
	cmd.Flags().StringVar(&projectDir, "project", "", "Open this directory in the IDE")
	cmd.Flags().BoolVar(&noTelemetry, "no-telemetry", false, "Disable session telemetry sidecar")
	cmd.Flags().StringVar(&nodeName, "node", "", "Pin session to this node (default: platform node from context)")
	cmd.Flags().StringVar(&backend, "backend", "docker", "Backend: docker (Nomad service job) or vm (Multipass VM)")

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
	// SSH handle for Caddy admin API calls (route registration after alloc starts).
	node := workbench.NodeSSHFromName(nodeName)

	// Resolve MinIO credentials from context.
	s3Endpoint, s3AccessKey, s3SecretKey := resolveS3Creds(ctx)

	// Ensure the homedir exists on the cluster node before submitting the job.
	// For the Docker bind-mount model, Nomad will refuse to start the container if
	// the host path does not exist. We create it via SSH to aither so the operator
	// does not need to manually provision per-user directories.
	homedirPath := "/data/workbench/" + user + "/home"
	if mkErr := ensureRemoteDir(ctx, nodeName, homedirPath); mkErr != nil {
		// Non-fatal: warn and continue — the job may still work if the dir was
		// created out-of-band, but most likely it will fail at the container start.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not create homedir on %s: %v\n", nodeName, mkErr)
	}

	// Generate session ID and token.
	// Token is derived deterministically from the MinIO secret key so it is stable
	// across sessions — users don't need to run 'abc workbench url' after every start.
	// Falls back to a random token if no S3 credentials are available.
	sessionID := state.NewWorkbenchID()
	token := workbench.DeriveToken(s3SecretKey)
	if token == workbench.DeriveToken("") {
		// Empty secret key produces a predictable token — use random instead.
		token = randomToken(16)
	}

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

	// Register the path-based Caddy route so the session is reachable via the
	// public HTTPS URL (same mechanism as the VM backend). If the allocation host
	// is 0.0.0.0 (bound on all interfaces), use 127.0.0.1 as the upstream address
	// — the workbench Caddy is co-located on the same node.
	upstreamHost := host
	if upstreamHost == "" || upstreamHost == "0.0.0.0" || upstreamHost == "::" {
		upstreamHost = "127.0.0.1"
	}
	upstreamAddr := upstreamHost + ":" + strconv.Itoa(port)
	if routeErr := workbench.RegisterWorkbenchRoute(node, user, upstreamAddr); routeErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "  warning: caddy route registration: %v\n", routeErr)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ✓ workbench route registered\n")
	}

	workbenchURL := fmt.Sprintf("https://workbench.seedling.abc-cluster.cloud/%s/", user)
	directURL := fmt.Sprintf("http://%s:%d", host, port)

	if isFirstSession {
		fmt.Printf(`Your workbench session is ready.

  Browser:  %s
  Direct:   %s  (Tailscale / aither network only)
  Password: %s

  Homedir:  /home/%s/            (persists across sessions on this node)
  MinIO:    s3://%s/             (pipeline outputs, uploaded data)

Run 'abc workbench url' to show credentials again.
Stop when done: abc workbench stop
`, workbenchURL, directURL, token, user, user)
	} else {
		fmt.Printf("Session ready.\n\n  %s\n  password: %s\n\nStop when done: abc workbench stop\n",
			workbenchURL, token)
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

// ensureRemoteDir creates a directory on the named Nomad node via SSH.
// It reads the aither SSH config from the active context (same path as abc doctor).
// Silently succeeds if the directory already exists.
func ensureRemoteDir(ctx config.Context, nodeName, path string) error {
	sshHost, ok := config.GetAdminFloorField(&ctx.Admin.Services, "ssh", nodeName)
	if !ok || sshHost == "" {
		// Fall back to the node name itself as the SSH hostname — works when
		// the node name matches the SSH Host alias in ~/.ssh/config (e.g. "sun-aither").
		sshHost = "sun-" + nodeName
	}
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		sshHost, "mkdir", "-p", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh %s mkdir -p %s: %w\n%s", sshHost, path, err, out)
	}
	return nil
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

// runStartVM handles 'abc workbench start --backend=vm'.
// It manages the Multipass VM lifecycle directly via SSH to the platform node,
// then writes an SSH config entry and prints the stable workbench URL.
func runStartVM(cmd *cobra.Command, cores, memMB, idleHours int, ide, projectDir string, noTelemetry bool, nodeName string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	user := strings.TrimSpace(ctx.Admin.Whoami)
	if user == "" && ctx.Auth != nil {
		user = strings.TrimSpace(ctx.Auth.Whoami)
	}
	if user == "" {
		return fmt.Errorf("cannot determine user: run 'abc auth whoami' or ensure active context has admin.whoami set")
	}

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

	_, priorErr := workbench.LatestSession(context.Background(), db, user)
	isFirstSession := errors.Is(priorErr, workbench.ErrNoSession)

	// Resolve platform node.
	if nodeName == "" {
		nc := utils.NomadClientFromConfig()
		nodeName, err = resolvePlatformNode(nc, ctx)
		if err != nil {
			// Default to aither when Nomad is not reachable (VM backend does not need Nomad).
			nodeName = "aither"
		}
	}

	node := workbench.NodeSSHFromName(nodeName)
	// Multipass VM names must match [a-z0-9][a-z0-9-]* — replace underscores
	// (present in pool-user pseudonyms like lunar_hornbill) with hyphens.
	vmName := "wb-" + strings.ReplaceAll(user, "_", "-")
	jumpHost := "sun-" + nodeName

	// Resolve token.
	_, _, s3SecretKey := resolveS3Creds(ctx)
	token := workbench.DeriveToken(s3SecretKey)
	if token == workbench.DeriveToken("") {
		token = randomToken(16)
	}

	w := cmd.ErrOrStderr()

	// Inspect VM state and bring it up.
	vmst, stErr := workbench.GetVMState(node, vmName)
	if stErr != nil {
		return fmt.Errorf("check VM state: %w", stErr)
	}

	switch vmst {
	case workbench.VMStateUnknown:
		// VM does not exist — full first-time setup.
		fmt.Fprintf(w, "Setting up your workbench (first time, ~5 min)...\n")
		if err := workbench.LaunchVM(node, vmName, cores, memMB, w); err != nil {
			return err
		}
		// Bind-mount the user's persistent homedir.
		// ensureRemoteDir creates the host-side directory (mkdir -p via SSH).
		// MountHomedir then asks multipass to bind-mount it into the VM.
		homedirPath := "/data/workbench/" + user + "/home"
		if mkErr := ensureRemoteDir(ctx, nodeName, homedirPath); mkErr != nil {
			fmt.Fprintf(w, "  warning: create homedir on %s: %v\n", nodeName, mkErr)
		}
		if mErr := workbench.MountHomedir(node, vmName, homedirPath, "/home/ubuntu"); mErr != nil {
			fmt.Fprintf(w, "  warning: mount homedir: %v\n", mErr)
		}
		if err := workbench.WaitForSSH(node, vmName); err != nil {
			return err
		}
		// Inject the local SSH public key so the user can connect via Remote SSH.
		if err := workbench.InjectSSHKey(node, vmName); err != nil {
			fmt.Fprintf(w, "  warning: inject SSH key: %v\n", err)
		}
		if err := workbench.ProvisionVM(node, vmName, w); err != nil {
			return err
		}
		fmt.Fprintf(w, "  ✓ VM created: %s\n", vmName)

	case workbench.VMStateSuspended, workbench.VMStateStopped:
		fmt.Fprintf(w, "Resuming %s...", vmName)
		if err := workbench.StartVM(node, vmName); err != nil {
			return err
		}
		if err := workbench.WaitForSSH(node, vmName); err != nil {
			return err
		}
		fmt.Fprintf(w, "  done\n")

	case workbench.VMStateRunning:
		// Already up — still (re)start services below to handle stale processes.
	}

	// Start (or restart) IDE services inside the VM.
	if err := workbench.StartWorkbenchServices(node, vmName, token, w); err != nil {
		return fmt.Errorf("start workbench services: %w", err)
	}

	// Resolve VM IP for SSH config and direct URL.
	vmIP, err := workbench.GetVMIP(node, vmName)
	if err != nil {
		return fmt.Errorf("get VM IP: %w", err)
	}

	// Register (or update) the /<user>/* → vmIP:8080 route in the workbench
	// Caddy via admin API. Idempotent: DELETE+POST replaces any stale route.
	if routeErr := workbench.RegisterWorkbenchRoute(node, user, vmIP+":8080"); routeErr != nil {
		// Non-fatal: the direct URL still works; warn so the user knows the
		// path-based URL won't work until the route is registered.
		fmt.Fprintf(w, "  warning: caddy route registration: %v\n", routeErr)
	} else {
		fmt.Fprintf(w, "  ✓ workbench route registered\n")
	}

	// Write ~/.ssh/config entry (idempotent; updates HostName if IP changed).
	newSSHEntry := !workbench.SSHConfigPresent(vmName)
	if sshErr := workbench.WriteSSHConfigEntry(vmName, vmIP, jumpHost); sshErr != nil {
		fmt.Fprintf(w, "  warning: could not write SSH config: %v\n", sshErr)
	} else if newSSHEntry {
		fmt.Fprintf(w, "  ✓ SSH config updated (~/.ssh/config)\n")
	}

	// Record the session.
	sessionID := state.NewWorkbenchID()
	sess := &workbench.Session{
		SessionID:       sessionID,
		User:            user,
		JobID:           vmName,  // "wb-<user>" — distinguishes VM from Docker sessions
		Namespace:       "vm",
		IDE:             ide,
		Host:            vmIP,
		Port:            8080,
		Token:           token,
		Cores:           cores,
		MemoryMB:        memMB,
		GPUs:            0,
		Telemetry:       !noTelemetry,
		IdleTimeoutSecs: idleHours * 3600,
		Status:          "starting",
	}
	if err := workbench.InsertSession(context.Background(), db, sess); err != nil {
		fmt.Fprintf(w, "  warning: could not record session: %v\n", err)
	} else {
		_ = workbench.UpdateRunning(context.Background(), db, sessionID, vmIP, 8080)
	}

	// Print user-facing output.
	// Path-based URL (stable after Caddy routing is configured):
	workbenchURL := fmt.Sprintf("https://workbench.seedling.abc-cluster.cloud/%s/", user)
	// Direct URL (accessible now via Tailscale from the node, or over the local net):
	directURL := fmt.Sprintf("http://%s:8080", vmIP)

	if isFirstSession || vmst == workbench.VMStateUnknown {
		fmt.Printf(`
Workbench ready.

  Browser:    %s
  Direct:     %s  (Tailscale / aither network only)
  Remote SSH: %s

Connect from VS Code or Positron:  Remote SSH → %s

Your files are at ~/  (persists across sessions)
             and s3://%s/  (pipeline outputs, uploaded data)

Stop when done: abc workbench stop
`, workbenchURL, directURL, vmName, vmName, user)
	} else {
		fmt.Printf("\nWorkbench ready.\n\n  %s\n  Remote SSH: %s\n\nStop when done: abc workbench stop\n",
			workbenchURL, vmName)
	}
	return nil
}
