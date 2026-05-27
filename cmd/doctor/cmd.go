// Package doctor implements the "abc doctor" command.
//
// abc doctor runs three ordered check groups and reports results:
//
//  1. Config   — loads ~/.abc/config.yaml, validates the active context,
//     checks that a Nomad address and access token are configured.
//  2. Connectivity — probes each configured service endpoint directly
//     (Nomad health, MinIO liveness, upload endpoint reachability).
//  3. Workload — submits a minimal raw_exec batch job (echo) as a probe,
//     waits for it to complete, then purges it. Proves the full submit→
//     schedule→run→complete path without requiring a container runtime.
//
// Flags:
//
//	--skip-job      skip the workload probe (groups 1+2 only)
//	--json          emit a single JSON object with all check results
//	--context <n>   run against a named context instead of the active one
//	-q / --quiet    already set on rootCmd; suppresses the mode banner
//
// Exit codes: 0 = all checks passed, 1 = one or more checks failed.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// NewCmd returns the "doctor" command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run end-to-end health checks on the active context",
		Long: `abc doctor verifies that the abc-cluster CLI is correctly configured and
that it can reach and submit work to the active cluster.

Three check groups run in order:

  1. Config       — config.yaml loads, active context is set, Nomad address
                    and access token are present.
  2. Connectivity — Nomad is reachable and healthy. MinIO is probed when an
                    S3 endpoint is configured in the active context.
  3. Workload     — a minimal raw_exec batch job is submitted as a probe, waits
                    up to 60 s for completion, then purged. Skip with --skip-job.

Exit code 0 = all checks passed. Exit code 1 = one or more failed.

Use 'abc cluster status' for a fast server-side service health table.
Use 'abc doctor' when you need to prove the full client→cluster path works.`,
		RunE: runDoctor,
	}

	cmd.Flags().Bool("skip-job", false, "Skip the workload probe (groups 1 and 2 only)")
	cmd.Flags().Bool("json", false, "Emit results as a single JSON object")
	cmd.Flags().String("context", "", "Run checks against this named context instead of the active one")
	cmd.Flags().Duration("job-timeout", 60*time.Second, "Maximum time to wait for the probe job to complete")

	return cmd
}

// ── Result types ──────────────────────────────────────────────────────────────

type checkStatus int

const (
	statusPass checkStatus = iota
	statusFail
	statusSkip
	statusWarn
)

func (s checkStatus) String() string {
	switch s {
	case statusPass:
		return "pass"
	case statusFail:
		return "fail"
	case statusSkip:
		return "skip"
	case statusWarn:
		return "warn"
	}
	return "unknown"
}

type checkResult struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

type groupResult struct {
	Name   string        `json:"name"`
	Checks []checkResult `json:"checks"`
}

// ── Printer helpers ────────────────────────────────────────────────────────────

func statusGlyph(s checkStatus) string {
	switch s {
	case statusPass:
		return "✓"
	case statusFail:
		return "✗"
	case statusSkip:
		return "–"
	case statusWarn:
		return "⚠"
	}
	return "?"
}

func printGroup(w *os.File, idx, total int, name string) {
	fmt.Fprintf(w, "\n  ━━━ %d / %d  %s\n\n", idx, total, name)
}

func printCheck(w *os.File, r checkResult) {
	detail := ""
	if r.Detail != "" {
		detail = "  " + r.Detail
	}
	fmt.Fprintf(w, "  %s  %-36s%s\n", statusGlyph(r.Status), r.Name, detail)
}

// ── Main command ──────────────────────────────────────────────────────────────

func runDoctor(cmd *cobra.Command, _ []string) error {
	skipJob, _ := cmd.Flags().GetBool("skip-job")
	asJSON, _ := cmd.Flags().GetBool("json")
	ctxName, _ := cmd.Flags().GetString("context")
	jobTimeout, _ := cmd.Flags().GetDuration("job-timeout")

	quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")

	out := os.Stdout
	errw := os.Stderr

	if !asJSON && !quiet {
		fmt.Fprintf(out, "\nabc doctor — end-to-end health check\n")
	}

	var groups []groupResult
	anyFail := false

	// ── Group 1: Config ───────────────────────────────────────────────────────
	g1, cfg, activeCtx := checkConfig(ctxName)
	groups = append(groups, g1)
	if !asJSON {
		printGroup(out, 1, totalGroups(skipJob), "Config")
		for _, c := range g1.Checks {
			printCheck(out, c)
		}
	}
	for _, c := range g1.Checks {
		if c.Status == statusFail {
			anyFail = true
		}
	}

	// ── Group 2: Connectivity ─────────────────────────────────────────────────
	g2 := checkConnectivity(cmd.Context(), cfg, activeCtx)
	groups = append(groups, g2)
	if !asJSON {
		printGroup(out, 2, totalGroups(skipJob), "Connectivity")
		for _, c := range g2.Checks {
			printCheck(out, c)
		}
	}
	for _, c := range g2.Checks {
		if c.Status == statusFail {
			anyFail = true
		}
	}

	// ── Group 3: Workload ─────────────────────────────────────────────────────
	if !skipJob {
		g3 := checkWorkload(cmd.Context(), cfg, activeCtx, jobTimeout, errw)
		groups = append(groups, g3)
		if !asJSON {
			printGroup(out, 3, totalGroups(skipJob), "Workload")
			for _, c := range g3.Checks {
				printCheck(out, c)
			}
		}
		for _, c := range g3.Checks {
			if c.Status == statusFail {
				anyFail = true
			}
		}
	}

	// ── JSON output ───────────────────────────────────────────────────────────
	if asJSON {
		type jsonOut struct {
			Groups []groupResult `json:"groups"`
			Passed bool          `json:"passed"`
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonOut{Groups: groups, Passed: !anyFail})
	}

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Fprintln(out)
	if anyFail {
		fmt.Fprintln(out, "  One or more checks failed. See details above.")
		return fmt.Errorf("doctor: one or more checks failed")
	}
	fmt.Fprintln(out, "  All checks passed. abc-cluster is healthy.")
	return nil
}

func totalGroups(skipJob bool) int {
	if skipJob {
		return 2
	}
	return 3
}

// ── Check group 1: Config ─────────────────────────────────────────────────────

func checkConfig(ctxName string) (groupResult, *config.Config, *config.Context) {
	g := groupResult{Name: "config"}

	// 1a. Load config.yaml
	cfg, err := config.Load()
	if err != nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "config.yaml loads",
			Status: statusFail,
			Detail: err.Error(),
		})
		return g, nil, nil
	}
	cfgPath := config.DefaultConfigPath()
	g.Checks = append(g.Checks, checkResult{
		Name:   "config.yaml loads",
		Status: statusPass,
		Detail: cfgPath,
	})

	// 1b. Active context
	if ctxName != "" {
		if _, ok := cfg.Contexts[ctxName]; !ok {
			g.Checks = append(g.Checks, checkResult{
				Name:   "context exists",
				Status: statusFail,
				Detail: fmt.Sprintf("no context named %q in config", ctxName),
			})
			return g, cfg, nil
		}
		cfg.ActiveContext = ctxName
	}
	if cfg.ActiveContext == "" {
		g.Checks = append(g.Checks, checkResult{
			Name:   "active context",
			Status: statusFail,
			Detail: "no active_context set — run 'abc auth login' or 'abc context add'",
		})
		return g, cfg, nil
	}
	g.Checks = append(g.Checks, checkResult{
		Name:   "active context",
		Status: statusPass,
		Detail: cfg.ActiveContext,
	})

	ctx := cfg.ActiveCtx()

	// 1c. Nomad address
	nomadAddr := ctx.NomadAddr()
	if nomadAddr == "" {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad address",
			Status: statusFail,
			Detail: "not set — run 'abc context add <name> --endpoint http://<ip>:4646'",
		})
	} else {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad address",
			Status: statusPass,
			Detail: nomadAddr,
		})
	}

	// 1d. Access token
	nomadToken := ctx.NomadToken()
	if nomadToken == "" {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad token",
			Status: statusWarn,
			Detail: "not set (anonymous; most operations require a token)",
		})
	} else {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad token",
			Status: statusPass,
			Detail: "present",
		})
	}

	// 1e. Upload endpoint (optional — warn if absent)
	uploadEP := strings.TrimSpace(ctx.UploadEndpoint)
	if ctx.Endpoint != "" && uploadEP == "" {
		// Derive from endpoint (same logic as config package default)
		uploadEP = strings.TrimRight(ctx.Endpoint, "/") + "/files/"
	}
	if uploadEP != "" {
		g.Checks = append(g.Checks, checkResult{
			Name:   "upload endpoint",
			Status: statusPass,
			Detail: uploadEP,
		})
	} else {
		g.Checks = append(g.Checks, checkResult{
			Name:   "upload endpoint",
			Status: statusSkip,
			Detail: "not configured",
		})
	}

	return g, cfg, &ctx
}

// ── Check group 2: Connectivity ───────────────────────────────────────────────

func checkConnectivity(ctx context.Context, cfg *config.Config, activeCtx *config.Context) groupResult {
	g := groupResult{Name: "connectivity"}

	if cfg == nil || activeCtx == nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad reachable",
			Status: statusSkip,
			Detail: "skipped (config check failed)",
		})
		return g
	}

	// 2a. Nomad health
	nomadAddr := activeCtx.NomadAddr()
	nomadToken := activeCtx.NomadToken()
	h := floor.ProbeNomad(ctx, nomadAddr, nomadToken)
	if h.Healthy {
		detail := nomadAddr
		if h.Detail != "" {
			detail = fmt.Sprintf("v%s (%s)", h.Detail, nomadAddr)
		}
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad reachable",
			Status: statusPass,
			Detail: detail,
		})
	} else {
		g.Checks = append(g.Checks, checkResult{
			Name:   "nomad reachable",
			Status: statusFail,
			Detail: fmt.Sprintf("%s: %s", nomadAddr, h.Detail),
		})
	}

	// 2b. Node count (informational — from NomadClient ListNodes)
	nc := utils.NewNomadClient(nomadAddr, nomadToken, activeCtx.NomadRegion())
	nodes, err := nc.ListNodes(ctx)
	if err == nil {
		ready := 0
		for _, n := range nodes {
			if strings.EqualFold(n.Status, "ready") {
				ready++
			}
		}
		g.Checks = append(g.Checks, checkResult{
			Name:   "nodes ready",
			Status: statusPass,
			Detail: fmt.Sprintf("%d / %d", ready, len(nodes)),
		})
	}

	// 2c. MinIO — probe if an S3 endpoint is configured
	minioEP := resolveMinioEndpoint(cfg, activeCtx)
	if minioEP != "" {
		mh := floor.ProbeMinIO(ctx, minioEP)
		st := statusPass
		if !mh.Healthy {
			st = statusFail
		}
		detail := minioEP
		if !mh.Healthy && mh.Detail != "" {
			detail = fmt.Sprintf("%s: %s", minioEP, mh.Detail)
		}
		g.Checks = append(g.Checks, checkResult{
			Name:   "minio reachable",
			Status: st,
			Detail: detail,
		})
	}

	return g
}

// resolveMinioEndpoint extracts a MinIO/S3 base URL from the active context.
// Returns "" when no S3 endpoint is configured.
func resolveMinioEndpoint(_ *config.Config, ctx *config.Context) string {
	if ctx == nil {
		return ""
	}
	// admin.services.minio.endpoint — populated by both abc-cluster and
	// abc-nodes tiers after config sync or manual set.
	if ctx.Admin.Services.MinIO != nil {
		if ep := strings.TrimSpace(ctx.Admin.Services.MinIO.Endpoint); ep != "" {
			return ep
		}
		if ep := strings.TrimSpace(ctx.Admin.Services.MinIO.HTTP); ep != "" {
			return ep
		}
	}
	return ""
}

// ── Check group 3: Workload ───────────────────────────────────────────────────

// doctorJobTTL is how long to let the probe job linger on Nomad before purging.
// We purge immediately on completion, but use this as a Nomad-side GC fallback.
const doctorJobTTL = 5 * time.Minute

func checkWorkload(ctx context.Context, cfg *config.Config, activeCtx *config.Context,
	timeout time.Duration, errw *os.File) groupResult {
	g := groupResult{Name: "workload"}

	if activeCtx == nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "probe job",
			Status: statusSkip,
			Detail: "skipped (config check failed)",
		})
		return g
	}

	nomadAddr := activeCtx.NomadAddr()
	nomadToken := activeCtx.NomadToken()
	nomadRegion := activeCtx.NomadRegion()
	namespace := activeCtx.NomadNamespace()
	if namespace == "" {
		namespace = "default"
	}

	// Build a unique job ID so concurrent doctor runs don't collide.
	jobID := fmt.Sprintf("abc-doctor-probe-%d", time.Now().UnixMilli()%100000)

	// Build the minimal inline batch job JSON. We use raw_exec (enabled on all
	// seedling-prod nodes per ADR-0058) so no container runtime is required.
	jobJSON, err := buildProbeJobJSON(jobID, namespace)
	if err != nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "build probe job",
			Status: statusFail,
			Detail: err.Error(),
		})
		return g
	}

	nc := utils.NewNomadClient(nomadAddr, nomadToken, nomadRegion)

	// Submit the job.
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "probe job submit",
			Status: statusFail,
			Detail: err.Error(),
		})
		return g
	}
	_ = resp // EvalID noted; we poll allocs directly
	g.Checks = append(g.Checks, checkResult{
		Name:   "probe job submit",
		Status: statusPass,
		Detail: jobID,
	})

	// Wait for the job to reach a terminal alloc state.
	allocID, allocStatus, waitErr := waitForProbeJobAlloc(ctx, nc, jobID, namespace, timeout, errw)

	// Always purge the probe job (best-effort).
	defer func() {
		purgeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = nc.StopJob(purgeCtx, jobID, namespace, true /* purge */)
	}()

	if waitErr != nil {
		g.Checks = append(g.Checks, checkResult{
			Name:   "probe job complete",
			Status: statusFail,
			Detail: waitErr.Error(),
		})
		return g
	}

	if !strings.EqualFold(allocStatus, "complete") {
		g.Checks = append(g.Checks, checkResult{
			Name:   "probe job complete",
			Status: statusFail,
			Detail: fmt.Sprintf("alloc %s ended with status %q (expected \"complete\")", shortID(allocID), allocStatus),
		})
		return g
	}

	g.Checks = append(g.Checks, checkResult{
		Name:   "probe job complete",
		Status: statusPass,
		Detail: fmt.Sprintf("alloc %s", shortID(allocID)),
	})
	return g
}

// waitForProbeJobAlloc polls Nomad until the probe job's allocation reaches a
// terminal state or the timeout elapses.
func waitForProbeJobAlloc(ctx context.Context, nc *utils.NomadClient,
	jobID, namespace string, timeout time.Duration, _ *os.File) (allocID, status string, err error) {

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("timed out after %s waiting for probe job allocation", timeout)
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		allocs, listErr := nc.GetJobAllocs(ctx, jobID, namespace, false)
		if listErr != nil {
			// Nomad may not have created the alloc yet; keep polling.
			time.Sleep(2 * time.Second)
			continue
		}
		for _, a := range allocs {
			if utils.AllocClientTerminalStatus(a.ClientStatus) {
				return a.ID, a.ClientStatus, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// buildProbeJobJSON constructs the Nomad job object JSON for the doctor probe.
// Uses raw_exec driver with a single-line echo so no container image is required.
//
// Important: utils.NomadClient.RegisterJob wraps the payload in {"Job": <json>}
// before posting to /v1/jobs, so this function returns only the job object —
// NOT a {"Job": {...}} wrapper.
func buildProbeJobJSON(jobID, namespace string) (json.RawMessage, error) {
	// We build the struct in Go and marshal to JSON directly — no HCL roundtrip
	// needed for such a trivial job.
	type task struct {
		Name      string         `json:"Name"`
		Driver    string         `json:"Driver"`
		Config    map[string]any `json:"Config"`
		Resources struct {
			CPU      int `json:"CPU"`
			MemoryMB int `json:"MemoryMB"`
		} `json:"Resources"`
	}
	type restartPolicy struct {
		Attempts int    `json:"Attempts"`
		Mode     string `json:"Mode"`
	}
	type taskGroup struct {
		Name          string        `json:"Name"`
		Count         int           `json:"Count"`
		RestartPolicy restartPolicy `json:"RestartPolicy"`
		Tasks         []task        `json:"Tasks"`
	}
	type job struct {
		ID          string      `json:"ID"`
		Name        string      `json:"Name"`
		Type        string      `json:"Type"`
		Namespace   string      `json:"Namespace"`
		NodePool    string      `json:"NodePool"`
		Datacenters []string    `json:"Datacenters"`
		TaskGroups  []taskGroup `json:"TaskGroups"`
	}

	t := task{
		Name:   "probe",
		Driver: "raw_exec",
		Config: map[string]any{
			"command": "/bin/sh",
			"args":    []string{"-c", "echo 'abc-doctor: ok'"},
		},
	}
	t.Resources.CPU = 100
	t.Resources.MemoryMB = 64

	j := job{
		ID:          jobID,
		Name:        jobID,
		Type:        "batch",
		Namespace:   namespace,
		NodePool:    "all",  // target nodes across all pools (platform, compute, etc.)
		Datacenters: []string{"*"},
		TaskGroups: []taskGroup{
			{
				Name:  "doctor",
				Count: 1,
				RestartPolicy: restartPolicy{
					Attempts: 0,
					Mode:     "fail",
				},
				Tasks: []task{t},
			},
		},
	}

	b, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("marshal probe job: %w", err)
	}
	return json.RawMessage(b), nil
}

// shortID returns the first 8 characters of a Nomad allocation/job UUID.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
