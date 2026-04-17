package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

const (
	// nodeProbeInstalledPath is the fallback location of abc-node-probe on cluster nodes.
	// The CLI will first attempt to download the latest release from GitHub, then fall back
	// to this path if download fails or the network is unavailable.
	nodeProbeInstalledPath  = "/opt/nomad/abc-node-probe"
	nodeProbeJobID          = "abc-node-probe-system"
	defaultProbeWaitTimeout = 5 * time.Minute
	defaultProbeWatchDelay  = 2 * time.Second

	// GitHub release details for abc-node-probe
	probeGitHubOwner = "abc-cluster"
	probeGitHubRepo  = "abc-node-probe"
	probeBinaryName  = "abc-node-probe"
)

var nodeProbeJobTemplate = template.Must(template.New("node_probe_job").Parse(`job {{printf "%q" .JobID}} {
	type        = "sysbatch"
	datacenters = [{{printf "%q" .Datacenter}}]

	parameterized {
		payload       = "forbidden"
		meta_optional = ["jurisdiction", "skip_categories", "json_only", "fail_fast"]
	}

	group "probe" {
		constraint {
			attribute = "${node.unique.id}"
			operator  = "="
			value     = {{printf "%q" .NodeID}}
		}

		restart {
			attempts = 0
			mode     = "fail"
		}

		task "probe" {
			driver = "raw_exec"

			config {
				command = "local/probe.sh"
				args    = {{.ArgsHCL}}
			}

			template {
				data = <<SCRIPT
#!/usr/bin/env bash
set -euo pipefail

BIN_PATH="${NOMAD_TASK_DIR}/abc-node-probe"
FALLBACK_PATH={{printf "%q" .FallbackPath}}
DOWNLOAD_URL={{printf "%q" .DownloadURL}}

download_via_https() {
  local url="$1"
  local out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 300 "$url" -o "$out"
  elif command -v wget >/dev/null 2>&1; then
    wget -q --tries=5 --timeout=30 -O "$out" "$url"
  else
    echo "abc-node-probe: need curl or wget to download release binary" >&2
    return 1
  fi
}

if [[ -n "${DOWNLOAD_URL}" ]]; then
  if [[ ! -x "${BIN_PATH}" ]]; then
    download_via_https "${DOWNLOAD_URL}" "${BIN_PATH}"
    chmod 755 "${BIN_PATH}"
  fi
fi

if [[ ! -x "${BIN_PATH}" ]] && [[ -x "${FALLBACK_PATH}" ]]; then
  BIN_PATH="${FALLBACK_PATH}"
fi

if [[ ! -x "${BIN_PATH}" ]]; then
  echo "abc-node-probe binary not available (DOWNLOAD_URL empty means no GitHub URL was embedded; install to ${FALLBACK_PATH} or fix release resolution)" >&2
  exit 1
fi

exec "${BIN_PATH}" "$@"
SCRIPT
				destination = "local/probe.sh"
				perms       = "0755"
			}

			resources {
				cpu    = 500
				memory = 512
			}
		}
	}
}
`))

func newProbeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "probe <node-id>",
		Short: "Run abc-node-probe on a specific node via a system batch job",
		Long: `Deploy and execute the abc-node-probe binary on a specific Nomad node.

This command registers a system-level parameterized Nomad job and dispatches it
to the specified node. The probe runs with --nomad-mode to exit cleanly (code 0)
regardless of probe results. The actual node readiness verdict is in the JSON
output and the summary section.

By default the job embeds a bootstrap script that downloads the latest matching
release asset for the node's OS/architecture from GitHub. If GitHub cannot be
resolved, the command fails unless you pass --installed-binary-only (expects
the binary at /opt/nomad/abc-node-probe on the node).

  abc infra compute probe nomad-client-02 --jurisdiction=ZA
  abc infra compute probe nomad-client-02 --platform=linux/arm64
  abc infra compute probe nomad-client-02 --installed-binary-only`,
		Args: cobra.ExactArgs(1),
		RunE: runProbe,
	}
	cmd.Flags().String("jurisdiction", utils.EnvOrDefault("ABC_PROBE_JURISDICTION"),
		"Optional jurisdiction passed to probe (ISO-3166 alpha-2)")
	cmd.Flags().String("skip-categories", "",
		"Optional comma-separated probe categories to skip")
	cmd.Flags().Bool("json", false, "Pass --json to probe for JSON-only output")
	cmd.Flags().Bool("fail-fast", false, "Pass --fail-fast to probe")
	cmd.Flags().Bool("detach", false, "Submit probe and return without waiting for logs")
	cmd.Flags().Duration("wait-timeout", defaultProbeWaitTimeout,
		"Maximum time to wait while streaming probe results")
	cmd.Flags().String("platform", "",
		"Override OS/arch for the downloaded probe binary (e.g. linux/arm64); default is inferred from Nomad node fingerprints")
	cmd.Flags().Bool("installed-binary-only", false,
		"Skip GitHub: only run the probe binary already installed at /opt/nomad/abc-node-probe (no download)")
	return cmd
}

func runProbe(cmd *cobra.Command, args []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}

	nc := nomadClientFromCmd(cmd)
	nodeRef := args[0]
	node, err := resolveNodeRef(cmd, nc, nodeRef)
	if err != nil {
		return err
	}

	goos, goarch, err := resolveProbePlatform(cmd, node)
	if err != nil {
		return err
	}

	installedOnly, _ := cmd.Flags().GetBool("installed-binary-only")
	var downloadURL, version string
	if installedOnly {
		downloadURL = ""
		version = "installed-only"
	} else {
		var errGH error
		downloadURL, version, errGH = utils.GetLatestReleaseAssetURLForPlatform(
			probeGitHubOwner, probeGitHubRepo, probeBinaryName, goos, goarch)
		if errGH != nil {
			return fmt.Errorf("resolve GitHub release asset for probe (%s/%s): %w\n\n"+
				"Hints: export GITHUB_TOKEN or GH_TOKEN if rate-limited; check --platform=os/arch; "+
				"or use --installed-binary-only when %q is already on the node",
				goos, goarch, errGH, nodeProbeInstalledPath)
		}
	}
	if version == "" {
		version = "unknown"
	}

	// Build probe command args from flags
	var probeArgs []string
	// Always use nomad-mode since we're running in a Nomad job context
	probeArgs = append(probeArgs, "--nomad-mode")
	probeArgs = append(probeArgs, "--mode=stdout")

	if v, _ := cmd.Flags().GetString("jurisdiction"); strings.TrimSpace(v) != "" {
		probeArgs = append(probeArgs, fmt.Sprintf("--jurisdiction=%s", strings.TrimSpace(v)))
	}
	if v, _ := cmd.Flags().GetString("skip-categories"); strings.TrimSpace(v) != "" {
		probeArgs = append(probeArgs, fmt.Sprintf("--skip-categories=%s", strings.TrimSpace(v)))
	}
	if v, _ := cmd.Flags().GetBool("json"); v {
		probeArgs = append(probeArgs, "--json")
	}
	if v, _ := cmd.Flags().GetBool("fail-fast"); v {
		probeArgs = append(probeArgs, "--fail-fast")
	}

	probeHCL := buildNodeProbeJobHCL(node.Datacenter, node.ID, nodeProbeInstalledPath, downloadURL, probeArgs)
	jobJSON, err := nc.ParseHCL(cmd.Context(), probeHCL)
	if err != nil {
		return fmt.Errorf("nomad HCL parse for %q: %w", nodeProbeJobID, err)
	}

	if err := nc.PreflightJobTaskDrivers(cmd.Context(), jobJSON, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if _, err := nc.RegisterJob(cmd.Context(), jobJSON); err != nil {
		return fmt.Errorf("registering probe job %q: %w", nodeProbeJobID, err)
	}

	meta := map[string]string{}
	// Note: parameterized job metadata is still accepted for backward compatibility,
	// but is no longer used since we invoke the binary directly with args.

	resp, err := nc.DispatchJob(cmd.Context(), nodeProbeJobID, meta, nil)
	if err != nil {
		return fmt.Errorf("dispatching probe job for node %q: %w", node.ID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  ✓ Probe dispatched\n")
	fmt.Fprintf(out, "  Node           %s (%s)\n", node.Name, shortID(node.ID))
	fmt.Fprintf(out, "  Node platform  %s/%s\n", goos, goarch)
	fmt.Fprintf(out, "  Nomad job ID   %s\n", resp.DispatchedJobID)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	fmt.Fprintf(out, "  Probe version  %s\n", version)

	detach, _ := cmd.Flags().GetBool("detach")
	if detach {
		return nil
	}

	waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")
	fmt.Fprintf(out, "\n  Streaming probe output...\n\n")
	if err := utils.WatchJobLogsForTask(cmd.Context(), nc, resp.DispatchedJobID, "", "probe", out, defaultProbeWatchDelay, waitTimeout); err != nil {
		return fmt.Errorf("streaming probe output: %w", err)
	}
	if err := reportProbeTaskOutcome(cmd.Context(), nc, cmd.ErrOrStderr(), resp.DispatchedJobID, "probe"); err != nil {
		return err
	}
	return nil
}

func resolveProbePlatform(cmd *cobra.Command, node *utils.NomadNode) (goos, goarch string, err error) {
	if p, _ := cmd.Flags().GetString("platform"); strings.TrimSpace(p) != "" {
		parts := strings.SplitN(strings.TrimSpace(p), "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", "", fmt.Errorf("--platform must be os/arch, e.g. linux/arm64")
		}
		return strings.TrimSpace(strings.ToLower(parts[0])), strings.TrimSpace(strings.ToLower(parts[1])), nil
	}
	return utils.NomadNodeReleasePlatform(node)
}

func reportProbeTaskOutcome(ctx context.Context, nc *utils.NomadClient, errOut io.Writer, jobID, task string) error {
	allocs, err := nc.GetJobAllocs(ctx, jobID, "", false)
	if err != nil {
		return fmt.Errorf("fetch allocations after probe: %w", err)
	}
	var latest *utils.NomadAllocStub
	for i := range allocs {
		a := &allocs[i]
		if latest == nil || a.CreateTime > latest.CreateTime {
			latest = a
		}
	}
	if latest == nil {
		return fmt.Errorf("no allocation found for probe job %q", jobID)
	}

	ts, ok := latest.TaskStates[task]
	taskFailed := ok && ts.Failed && (strings.EqualFold(ts.State, "dead") || strings.EqualFold(ts.State, "failed"))
	allocFailed := strings.EqualFold(latest.ClientStatus, "failed")

	if !taskFailed && !allocFailed {
		return nil
	}

	_, _ = fmt.Fprintf(errOut, "\n--- probe stderr ---\n")
	_ = nc.StreamLogs(ctx, latest.ID, task, "stderr", "start", 0, false, errOut)

	if allocFailed && ok {
		return fmt.Errorf("probe failed (allocation client_status=%s, task %q state=%s failed=%v)",
			latest.ClientStatus, task, ts.State, ts.Failed)
	}
	if allocFailed {
		return fmt.Errorf("probe failed (allocation client_status=%s)", latest.ClientStatus)
	}
	return fmt.Errorf("probe task %q failed (state=%s)", task, ts.State)
}

func resolveNodeRef(cmd *cobra.Command, nc *utils.NomadClient, ref string) (*utils.NomadNode, error) {
	n, err := nc.GetNode(cmd.Context(), ref)
	if err == nil {
		return n, nil
	}

	nodes, listErr := nc.ListNodes(cmd.Context())
	if listErr != nil {
		return nil, fmt.Errorf("resolving node %q: %w", ref, listErr)
	}

	matches := make([]utils.NomadNodeStub, 0, 4)
	for _, node := range nodes {
		if node.Name == ref || strings.HasPrefix(node.ID, ref) {
			matches = append(matches, node)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("node %q not found", ref)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, fmt.Sprintf("%s (%s)", m.Name, shortID(m.ID)))
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("node %q is ambiguous: %s", ref, strings.Join(ids, ", "))
	}

	resolved, getErr := nc.GetNode(cmd.Context(), matches[0].ID)
	if getErr != nil {
		return nil, fmt.Errorf("fetching node %q: %w", matches[0].ID, getErr)
	}
	return resolved, nil
}

func buildNodeProbeJobHCL(datacenter, nodeID, fallbackProbePath, downloadURL string, probeArgs []string) string {
	datacenter = strings.TrimSpace(datacenter)
	if datacenter == "" {
		datacenter = "dc1"
	}
	nodeID = strings.TrimSpace(nodeID)
	fallbackProbePath = strings.TrimSpace(fallbackProbePath)
	if fallbackProbePath == "" {
		fallbackProbePath = nodeProbeInstalledPath
	}

	argsHCL := "[]"
	if len(probeArgs) > 0 {
		if b, err := json.Marshal(probeArgs); err == nil {
			argsHCL = string(b)
		}
	}

	data := struct {
		JobID        string
		Datacenter   string
		NodeID       string
		ArgsHCL      string
		DownloadURL  string
		FallbackPath string
	}{
		JobID:        nodeProbeJobID,
		Datacenter:   datacenter,
		NodeID:       nodeID,
		ArgsHCL:      argsHCL,
		DownloadURL:  strings.TrimSpace(downloadURL),
		FallbackPath: fallbackProbePath,
	}

	var b bytes.Buffer
	if err := nodeProbeJobTemplate.Execute(&b, data); err != nil {
		// Template is statically validated with Must; keep a defensive fallback.
		panic(err)
	}
	return b.String()
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
