package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/compute"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// "abc cluster configuration" — manage per-node configuration captured from
// abc-node-probe and stored in the active context's config.yaml under
// capabilities.nodes[i].probe.
//
// Sibling of "abc cluster capabilities". Where capabilities sync is the
// cheap "what services are running across the cluster" view, configuration
// sync is the deep "what does ONE node look like" view — sourced from
// abc-node-probe's JSON report and persisted alongside that node's existing
// driver/volume metadata.

func newConfigurationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configuration",
		Short: "Capture and store per-node configuration from abc-node-probe",
		Long: `Commands for syncing the deep, per-node configuration snapshot from abc-node-probe
into the active context's config.yaml.

  abc cluster configuration sync --id <nomad-client-node-id>
  abc cluster configuration show [--id <nomad-client-node-id>]

Each sync runs abc-node-probe in --json mode against the named node and stores
the resulting report at  contexts.<active>.capabilities.nodes[i].probe.  The
report is preserved as raw JSON for forward compatibility (probe schema
evolves between releases) plus a few pre-extracted fields (collected_at,
probe_version, severity, jurisdiction).`,
	}
	cmd.AddCommand(newConfigurationSyncCmd(), newConfigurationShowCmd())
	return cmd
}

func newConfigurationSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync per-node configuration into config (basic info by default; --probe for full report)",
		Long: `Refreshes  contexts.<active>.capabilities.nodes[]  in config.yaml from Nomad.

Modes (orthogonal — combine freely):

  Default (no --probe)
    Cheap basic info only — same per-node data that "abc cluster capabilities sync"
    populates: ID, hostname, healthy+detected drivers, host volumes.  Comes
    straight from the Nomad /v1/node/<id> API; no jobs are dispatched.

  --probe
    Additionally runs the abc-node-probe binary on each node as a Nomad
    sysbatch job, captures the --json report, and stores it under
    nodes[i].probe (with collected_at, version, severity, jurisdiction,
    plus the full raw JSON).  Slow (~30-60s per node) but deep — hardware,
    OS, security posture, compliance, etc.  Probe failures on individual
    nodes are reported but don't abort the sweep.

Scope (orthogonal to --probe):

  No --id   →  every ready+eligible+non-draining Nomad client node.
  --id REF  →  exactly one node, resolved by UUID prefix or hostname.

Examples:
  abc cluster configuration sync                                # all nodes, basic info
  abc cluster configuration sync --id 4f6b45a7                  # one node, basic info
  abc cluster configuration sync --probe                        # all nodes, basic + probe
  abc cluster configuration sync --probe --id 4f6b45a7          # one node, basic + probe
  abc cluster configuration sync --probe --jurisdiction=ZA      # all + compliance scope
  abc cluster configuration sync --probe --id nomad02 --installed-binary-only`,
		RunE: runConfigurationSync,
	}
	cmd.Flags().String("id", "",
		"Nomad client node UUID prefix or hostname; if omitted, sync all eligible nodes")
	cmd.Flags().Bool("probe", false,
		"Also run abc-node-probe on each node and store the JSON report under nodes[i].probe (slow, opt-in)")

	// Mirror the relevant flags from "abc infra compute probe" so this command
	// is self-contained — operators don't have to know about probe.go internals.
	cmd.Flags().String("jurisdiction", utils.EnvOrDefault("ABC_PROBE_JURISDICTION"),
		"ISO 3166-1 alpha-2 country code passed to probe (REQUIRED for compliance checks)")
	cmd.Flags().String("skip-categories", "",
		"Comma-separated probe categories to skip")
	cmd.Flags().Bool("fail-fast", false, "Pass --fail-fast to probe")
	cmd.Flags().Duration("wait-timeout", compute.DefaultProbeWaitTimeout,
		"Maximum time to wait while collecting probe output")
	cmd.Flags().String("platform", "",
		"Override OS/arch for the downloaded probe binary (e.g. linux/arm64); default is inferred from Nomad node fingerprints")
	cmd.Flags().Bool("installed-binary-only", false,
		"Skip GitHub: only run the probe binary already installed at /opt/nomad/abc-node-probe (no download)")
	cmd.Flags().String("release-url-base", utils.EnvOrDefault("ABC_PROBE_RELEASE_URL_BASE"),
		"If set, fetch the probe binary from this URL prefix instead of GitHub. The full URL is "+
			"<base>/abc-node-probe-<os>-<arch>. Useful for clusters that mirror releases on RustFS / Garage "+
			"(e.g. http://rustfs.aither/releases/abc-node-probe/v0.1.4) to avoid GitHub rate limits.")
	return cmd
}

func newConfigurationShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the stored probe report for a node (or list all if --id omitted)",
		RunE:  runConfigurationShow,
	}
	cmd.Flags().String("id", "",
		"Nomad client node ID or hostname; if omitted, list all nodes that have a stored probe")
	cmd.Flags().Bool("raw", false, "Print the raw JSON report (default: pre-extracted summary fields)")
	return cmd
}

func runConfigurationSync(cmd *cobra.Command, _ []string) error {
	nodeRef, _ := cmd.Flags().GetString("id")
	nodeRef = strings.TrimSpace(nodeRef)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return fmt.Errorf("cannot resolve active context %q", cfg.ActiveContext)
	}
	ctxStored := cfg.Contexts[ctxName]

	nc, err := nomadClientForCapabilities(cmd)
	if err != nil {
		return err
	}

	// ── 1. Resolve target node(s). ───────────────────────────────────────────
	// nodeRef set → exactly one node.  Otherwise, all ready+eligible+non-draining
	// Nomad clients (mirrors syncNodeCapabilities filter in capabilities.go).
	var targets []*utils.NomadNode
	if nodeRef != "" {
		node, err := compute.ResolveNodeRef(cmd, nc, nodeRef)
		if err != nil {
			return err
		}
		targets = []*utils.NomadNode{node}
	} else {
		targets, err = enumerateEligibleNodes(cmd.Context(), nc)
		if err != nil {
			return fmt.Errorf("enumerate cluster nodes: %w", err)
		}
		if len(targets) == 0 {
			return fmt.Errorf("no ready/eligible Nomad client nodes found in fleet")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Probing %d node(s) (use --id to target a single node):\n", len(targets))
	}

	withProbe, _ := cmd.Flags().GetBool("probe")

	// ── 2. Resolve probe options once (only used when --probe is set). ──────
	var opts *probeOptions
	if withProbe {
		opts, err = buildProbeOptionsFromCmd(cmd)
		if err != nil {
			return err
		}
	}

	// ── 3. Walk targets.  Every iteration ALWAYS refreshes basic node info
	// (drivers, volumes — same fields populated by `capabilities sync`).  When
	// --probe is set, we additionally dispatch the abc-node-probe Nomad job
	// per node and populate nodes[i].probe.  Failures on individual nodes are
	// warnings, not fatal — the rest of the fleet still gets refreshed.  Save
	// once at the end so the file lands in a single consistent state.
	caps := ctxStored.Capabilities
	if caps == nil {
		caps = &config.Capabilities{}
	}

	out := cmd.OutOrStdout()
	mode := "basic"
	if withProbe {
		mode = "basic + probe"
	}
	if len(targets) > 1 {
		fmt.Fprintf(out, "Mode: %s.\n", mode)
	}

	var (
		basicOK    int
		probeOK    int
		probeFails []string
	)
	for i, node := range targets {
		if len(targets) > 1 {
			fmt.Fprintf(out, "\n[%d/%d] %s (%s)\n", i+1, len(targets), node.Name, compute.ShortID(node.ID))
		}

		// ── basic: drivers + volumes from the GetNode payload we already have.
		drivers, volumes := extractNodeBasicInfo(node)
		upsertNodeBasicInfo(caps, node, drivers, volumes)
		basicOK++
		fmt.Fprintf(out, "  ✓ basic   drivers=%d  volumes=%d\n", len(drivers), len(volumes))

		// ── probe: only when --probe is set.
		if !withProbe {
			continue
		}
		report, err := probeOneNode(cmd, nc, node, opts)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ probe: %v\n", err)
			probeFails = append(probeFails, fmt.Sprintf("%s (%s): %v", node.Name, compute.ShortID(node.ID), err))
			continue
		}
		upsertNodeProbeReport(caps, node, report)
		probeOK++
		sev := report.Severity
		if sev == "" {
			sev = "(none)"
		}
		fmt.Fprintf(out, "  ✓ probe   severity=%s  version=%s\n", sev, report.ProbeVersion)
	}

	caps.LastSynced = time.Now().UTC()
	ctxStored.Capabilities = caps
	cfg.Contexts[ctxName] = ctxStored
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if len(targets) > 1 {
		if withProbe {
			fmt.Fprintf(out, "\nDone. basic: %d/%d  probe: %d/%d\n",
				basicOK, len(targets), probeOK, len(targets))
		} else {
			fmt.Fprintf(out, "\nDone. basic: %d/%d\n", basicOK, len(targets))
		}
	}
	if withProbe && len(probeFails) > 0 {
		// Probe failures are surfaced for visibility but don't fail the
		// command — the basic info still persisted, and partial probes are
		// often the right outcome on a heterogeneous cluster.
		fmt.Fprintf(cmd.ErrOrStderr(), "Probe failed on %d node(s):\n  - %s\n",
			len(probeFails), strings.Join(probeFails, "\n  - "))
	}
	return nil
}

// extractNodeBasicInfo pulls the cheap "what's running" fields out of a
// already-fetched Nomad node detail.  Same logic as in
// cmd/cluster/capabilities.go:syncNodeCapabilities — kept as a small helper so
// both `capabilities sync` and `configuration sync` produce identical entries.
func extractNodeBasicInfo(node *utils.NomadNode) (drivers, volumes []string) {
	for name, info := range node.Drivers {
		if info.Detected && info.Healthy {
			drivers = append(drivers, name)
		}
	}
	sort.Strings(drivers)
	for name, vol := range node.HostVolumes {
		entry := name + ":" + vol.Path
		if vol.ReadOnly {
			entry += " (ro)"
		}
		volumes = append(volumes, entry)
	}
	sort.Strings(volumes)
	return drivers, volumes
}

// upsertNodeBasicInfo updates an existing nodes[] entry's drivers/volumes/hostname
// (or appends one) without touching its Probe field.  Pairs with
// upsertNodeProbeReport, which conversely leaves drivers/volumes untouched.
func upsertNodeBasicInfo(caps *config.Capabilities, node *utils.NomadNode, drivers, volumes []string) {
	for i := range caps.Nodes {
		if caps.Nodes[i].ID == node.ID {
			caps.Nodes[i].Hostname = node.Name
			caps.Nodes[i].Drivers = drivers
			caps.Nodes[i].Volumes = volumes
			return
		}
	}
	caps.Nodes = append(caps.Nodes, config.NodeCapability{
		ID:       node.ID,
		Hostname: node.Name,
		Drivers:  drivers,
		Volumes:  volumes,
	})
	sort.Slice(caps.Nodes, func(i, j int) bool {
		return caps.Nodes[i].Hostname < caps.Nodes[j].Hostname
	})
}

// probeOptions carries the per-sync settings shared across nodes when running
// the fleet-wide path.  Resolved once from cobra flags by buildProbeOptionsFromCmd.
type probeOptions struct {
	platformOverride string        // raw --platform (empty = auto from node attrs)
	releaseBase      string        // --release-url-base (already TrimRight'd)
	installedOnly    bool          // --installed-binary-only
	jurisdiction     string        // --jurisdiction
	skipCategories   string        // --skip-categories
	failFast         bool          // --fail-fast
	waitTimeout      time.Duration // --wait-timeout
}

func buildProbeOptionsFromCmd(cmd *cobra.Command) (*probeOptions, error) {
	o := &probeOptions{}
	o.platformOverride, _ = cmd.Flags().GetString("platform")
	o.installedOnly, _ = cmd.Flags().GetBool("installed-binary-only")
	o.failFast, _ = cmd.Flags().GetBool("fail-fast")
	o.waitTimeout, _ = cmd.Flags().GetDuration("wait-timeout")
	if rb, _ := cmd.Flags().GetString("release-url-base"); rb != "" {
		o.releaseBase = strings.TrimRight(strings.TrimSpace(rb), "/")
	}
	if j, _ := cmd.Flags().GetString("jurisdiction"); j != "" {
		o.jurisdiction = strings.TrimSpace(j)
	}
	if v, _ := cmd.Flags().GetString("skip-categories"); v != "" {
		o.skipCategories = strings.TrimSpace(v)
	}
	return o, nil
}

// probeOneNode runs the abc-node-probe job against a single Nomad client node
// and returns the parsed JSON report.  Used by both the single-node (--id)
// and fleet-wide paths in runConfigurationSync — keep the per-node logic here
// so both paths get the same diagnostics, retry behaviour, and side effects.
func probeOneNode(cmd *cobra.Command, nc *utils.NomadClient, node *utils.NomadNode, o *probeOptions) (*config.NodeProbeReport, error) {
	// ── platform + download URL ──────────────────────────────────────────────
	goos, goarch, err := compute.ResolveProbePlatform(cmd, node)
	if err != nil {
		return nil, err
	}
	var downloadURL, version string
	switch {
	case o.installedOnly:
		version = "installed-only"
	case o.releaseBase != "":
		// Cluster-mirror path — skip GitHub.  URL convention matches what the
		// `just release` script publishes to RustFS:
		//   <base>/abc-node-probe-<os>-<arch>[.exe]
		ext := ""
		if goos == "windows" {
			ext = ".exe"
		}
		downloadURL = fmt.Sprintf("%s/abc-node-probe-%s-%s%s", o.releaseBase, goos, goarch, ext)
		// Best-effort tag extraction: if the base ends in /vX.Y.Z, capture it.
		if i := strings.LastIndex(o.releaseBase, "/"); i >= 0 {
			tail := o.releaseBase[i+1:]
			if strings.HasPrefix(tail, "v") {
				version = tail
			}
		}
		if version == "" {
			version = "release-base"
		}
	default:
		downloadURL, version, err = utils.GetLatestReleaseAssetURLForPlatform(
			compute.ProbeGitHubOwner, compute.ProbeGitHubRepo, compute.ProbeBinaryName, goos, goarch)
		if err != nil {
			return nil, fmt.Errorf("resolve GitHub release asset (%s/%s): %w\n\n"+
				"Hints:\n"+
				"  • export GITHUB_TOKEN or GH_TOKEN if rate-limited\n"+
				"  • use --release-url-base=http://rustfs.aither/releases/abc-node-probe/v0.1.4 "+
				"to fetch from the cluster RustFS mirror (no GitHub)\n"+
				"  • or --installed-binary-only when %q is already on the node",
				goos, goarch, err, compute.NodeProbeInstalledPath)
		}
	}
	if version == "" {
		version = "unknown"
	}

	// ── probe args ──────────────────────────────────────────────────────────
	probeArgs := []string{"--nomad-mode", "--mode=stdout", "--evaluate", "--json"}
	if o.jurisdiction != "" {
		probeArgs = append(probeArgs, fmt.Sprintf("--jurisdiction=%s", o.jurisdiction))
	}
	if o.skipCategories != "" {
		probeArgs = append(probeArgs, fmt.Sprintf("--skip-categories=%s", o.skipCategories))
	}
	if o.failFast {
		probeArgs = append(probeArgs, "--fail-fast")
	}

	// ── dispatch ────────────────────────────────────────────────────────────
	probeHCL := compute.BuildNodeProbeJobHCL(node.Datacenter, node.ID, compute.NodeProbeInstalledPath, downloadURL, probeArgs)
	jobJSON, err := nc.ParseHCL(cmd.Context(), probeHCL)
	if err != nil {
		return nil, fmt.Errorf("nomad HCL parse for %q: %w", compute.NodeProbeJobID, err)
	}
	if err := nc.PreflightJobTaskDrivers(cmd.Context(), jobJSON, cmd.ErrOrStderr()); err != nil {
		return nil, err
	}
	if _, err := nc.RegisterJob(cmd.Context(), jobJSON); err != nil {
		return nil, fmt.Errorf("registering probe job %q: %w", compute.NodeProbeJobID, err)
	}
	resp, err := nc.DispatchJob(cmd.Context(), compute.NodeProbeJobID, map[string]string{}, nil)
	if err != nil {
		return nil, fmt.Errorf("dispatching probe job: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  ✓ Probe dispatched   platform=%s/%s  version=%s  job=%s\n",
		goos, goarch, version, resp.DispatchedJobID)

	// ── wait + read stdout ──────────────────────────────────────────────────
	// We bypass utils.WatchJobLogsForTask* here: those helpers are tuned for
	// long-running tasks (they re-read only STDERR from start after termination
	// — see nomad_client.go:1081 — which loses STDOUT for fast-completing batch
	// jobs like ours).  We instead poll GetJobAllocs until terminal then
	// StreamLogs(stdout, origin=start) to fetch the FULL stdout buffer.
	allocID, err := waitForProbeAlloc(cmd.Context(), nc, resp.DispatchedJobID, o.waitTimeout)
	if err != nil {
		return nil, err
	}
	if err := compute.ReportProbeTaskOutcome(cmd.Context(), nc, cmd.ErrOrStderr(), resp.DispatchedJobID, "probe"); err != nil {
		return nil, err
	}
	var stdoutBuf bytes.Buffer
	if _, err := nc.StreamLogs(cmd.Context(), allocID, "probe", "stdout", "start", 0, false, &stdoutBuf); err != nil {
		return nil, fmt.Errorf("read probe stdout: %w", err)
	}
	raw, err := extractProbeJSON(stdoutBuf.Bytes())
	if err != nil {
		var stderrBuf bytes.Buffer
		_, _ = nc.StreamLogs(cmd.Context(), allocID, "probe", "stderr", "start", 0, false, &stderrBuf)
		preview := stdoutBuf.String()
		if len(preview) > 1500 {
			preview = preview[:1500] + "...(truncated)"
		}
		stderrPrev := stderrBuf.String()
		if len(stderrPrev) > 600 {
			stderrPrev = stderrPrev[:600] + "...(truncated)"
		}
		return nil, fmt.Errorf("parse probe JSON: %w\n\nSTDOUT (first 1500B):\n%s\n\nSTDERR (first 600B):\n%s",
			err, preview, stderrPrev)
	}
	return &config.NodeProbeReport{
		CollectedAt:  time.Now().UTC(),
		ProbeVersion: version,
		Severity:     extractTopSeverity(raw),
		Jurisdiction: o.jurisdiction,
		Raw:          raw,
	}, nil
}

// enumerateEligibleNodes returns the same set of nodes that
// "abc cluster capabilities sync" considers (status=ready, eligible, not draining).
// Mirrors the filter at cmd/cluster/capabilities.go:syncNodeCapabilities so the
// two commands populate the same nodes[] entries.
func enumerateEligibleNodes(ctx context.Context, nc *utils.NomadClient) ([]*utils.NomadNode, error) {
	stubs, err := nc.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var out []*utils.NomadNode
	for _, s := range stubs {
		if !strings.EqualFold(s.Status, "ready") {
			continue
		}
		if strings.EqualFold(s.SchedulingEligibility, "ineligible") {
			continue
		}
		if s.Drain {
			continue
		}
		node, err := nc.GetNode(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("get node %s: %w", s.ID, err)
		}
		out = append(out, node)
	}
	// Stable order: by hostname so output is predictable run-to-run.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// upsertNodeProbeReport finds the node entry in caps.Nodes by ID (or appends
// a new one if missing), and sets its Probe field. Hostname/Drivers/Volumes
// from a previous capabilities sync are preserved.
func upsertNodeProbeReport(caps *config.Capabilities, node *utils.NomadNode, report *config.NodeProbeReport) {
	for i := range caps.Nodes {
		if caps.Nodes[i].ID == node.ID {
			caps.Nodes[i].Probe = report
			if caps.Nodes[i].Hostname == "" {
				caps.Nodes[i].Hostname = node.Name
			}
			return
		}
	}
	caps.Nodes = append(caps.Nodes, config.NodeCapability{
		ID:       node.ID,
		Hostname: node.Name,
		Probe:    report,
	})
	sort.Slice(caps.Nodes, func(i, j int) bool {
		return caps.Nodes[i].Hostname < caps.Nodes[j].Hostname
	})
}

// waitForProbeAlloc polls GetJobAllocs until the dispatched job's allocation
// reaches a terminal client status (or the timeout fires), then returns its
// allocation ID.  Used in lieu of WatchJobLogsForTask* whose stdout-after-
// termination handling is incomplete for fast-finishing batch tasks.
func waitForProbeAlloc(ctx context.Context, nc *utils.NomadClient, jobID string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = compute.DefaultProbeWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for probe alloc on job %q (after %s)", jobID, timeout)
		}
		allocs, err := nc.GetJobAllocs(ctx, jobID, "", false)
		if err != nil {
			return "", fmt.Errorf("list allocs for %q: %w", jobID, err)
		}
		var latest *utils.NomadAllocStub
		for i := range allocs {
			a := &allocs[i]
			if latest == nil || a.CreateTime > latest.CreateTime {
				latest = a
			}
		}
		if latest != nil && utils.AllocClientTerminalStatus(latest.ClientStatus) {
			return latest.ID, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(compute.DefaultProbeWatchDelay):
		}
	}
}

// extractProbeJSON pulls the first top-level JSON object from a stream of
// probe stdout. abc-node-probe in --json mode emits exactly one object;
// the helper is forgiving of leading/trailing whitespace or stray banner
// lines emitted by the bootstrap script before the binary takes over.
func extractProbeJSON(buf []byte) (map[string]interface{}, error) {
	// Find the first '{' and decode from there with a streaming decoder so we
	// stop at the matching '}', tolerating any trailing content.
	idx := bytes.IndexByte(buf, '{')
	if idx < 0 {
		return nil, fmt.Errorf("no JSON object found in probe output (%d bytes)", len(buf))
	}
	dec := json.NewDecoder(bytes.NewReader(buf[idx:]))
	var out map[string]interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("decoded JSON is empty")
	}
	return out, nil
}

// extractTopSeverity returns the highest-severity verdict the probe reports.
// Looks first for the conventional top-level / summary.severity fields, then
// falls back to walking results[].severity and picking the worst.
// Severity ranking (worst → best): FAIL > WARN > PASS > INFO > SKIP.
// Returns empty if nothing parseable — cheap pre-extraction; Raw is the
// source of truth.
func extractTopSeverity(raw map[string]interface{}) string {
	if s, ok := raw["severity"].(string); ok && s != "" {
		return s
	}
	if summary, ok := raw["summary"].(map[string]interface{}); ok {
		if s, ok := summary["severity"].(string); ok && s != "" {
			return s
		}
	}
	// Walk results[] and pick the worst severity seen.
	results, ok := raw["results"].([]interface{})
	if !ok {
		return ""
	}
	rank := map[string]int{"FAIL": 4, "WARN": 3, "PASS": 2, "INFO": 1, "SKIP": 0}
	worst := ""
	worstRank := -1
	for _, r := range results {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		s, ok := m["severity"].(string)
		if !ok || s == "" {
			continue
		}
		s = strings.ToUpper(s)
		if rk, known := rank[s]; known && rk > worstRank {
			worst, worstRank = s, rk
		}
	}
	return worst
}

// ── show ───────────────────────────────────────────────────────────────────

func runConfigurationShow(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return fmt.Errorf("cannot resolve active context %q", cfg.ActiveContext)
	}
	ctx := cfg.Contexts[ctxName]
	if ctx.Capabilities == nil || len(ctx.Capabilities.Nodes) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  No probe reports stored for context %q.\n", ctxName)
		fmt.Fprintf(cmd.OutOrStdout(), "  Run: abc cluster configuration sync --id <node>\n")
		return nil
	}

	idFlag, _ := cmd.Flags().GetString("id")
	idFlag = strings.TrimSpace(idFlag)
	rawFlag, _ := cmd.Flags().GetBool("raw")

	w := cmd.OutOrStdout()
	if idFlag == "" {
		// Listing mode.
		fmt.Fprintf(w, "Stored probe reports (context %q):\n", ctxName)
		any := false
		for _, n := range ctx.Capabilities.Nodes {
			if n.Probe == nil {
				continue
			}
			any = true
			fmt.Fprintf(w, "  - %-20s  id=%s  severity=%s  collected=%s  version=%s\n",
				n.Hostname, compute.ShortID(n.ID),
				orNoneStr(n.Probe.Severity),
				n.Probe.CollectedAt.Format(time.RFC3339),
				orNoneStr(n.Probe.ProbeVersion))
		}
		if !any {
			fmt.Fprintf(w, "  (no nodes have a stored probe yet)\n")
		}
		return nil
	}

	// Single-node mode.
	for _, n := range ctx.Capabilities.Nodes {
		if !(n.ID == idFlag || strings.HasPrefix(n.ID, idFlag) || n.Hostname == idFlag) {
			continue
		}
		if n.Probe == nil {
			fmt.Fprintf(w, "  Node %s (%s) has no probe report yet.\n", n.Hostname, compute.ShortID(n.ID))
			fmt.Fprintf(w, "  Run: abc cluster configuration sync --id %s\n", n.ID)
			return nil
		}
		if rawFlag {
			b, err := json.MarshalIndent(n.Probe.Raw, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(w, string(b))
			return nil
		}
		fmt.Fprintf(w, "Probe report for %s (%s):\n", n.Hostname, n.ID)
		fmt.Fprintf(w, "  collected_at  %s\n", n.Probe.CollectedAt.Format(time.RFC3339))
		fmt.Fprintf(w, "  probe_version %s\n", orNoneStr(n.Probe.ProbeVersion))
		fmt.Fprintf(w, "  severity      %s\n", orNoneStr(n.Probe.Severity))
		if n.Probe.Jurisdiction != "" {
			fmt.Fprintf(w, "  jurisdiction  %s\n", n.Probe.Jurisdiction)
		}
		fmt.Fprintf(w, "  raw fields:   %d top-level keys (use --raw to dump JSON)\n", len(n.Probe.Raw))
		return nil
	}
	return fmt.Errorf("no stored probe matches id %q (try `abc cluster configuration show` to list)", idFlag)
}

// ── tiny helpers ──────────────────────────────────────────────────────────

func orNoneStr(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// silence unused-import in case some symbol is conditionally compiled out.
var _ = context.Background
