package compute

import (
	"bytes"
	"encoding/json"
	"fmt"
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
				command = {{printf "%q" .Command}}
				args    = {{.ArgsHCL}}
			}

{{- if .DownloadURL }}
			template {
				data = <<SCRIPT
#!/usr/bin/env bash
set -euo pipefail

BIN_PATH="${NOMAD_TASK_DIR}/abc-node-probe"
FALLBACK_PATH="/opt/nomad/abc-node-probe"
DOWNLOAD_URL={{printf "%q" .DownloadURL}}

if [ ! -x "$BIN_PATH" ]; then
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 300 "$DOWNLOAD_URL" -o "$BIN_PATH" || true
  elif command -v wget >/dev/null 2>&1; then
    wget -q --tries=5 --timeout=30 -O "$BIN_PATH" "$DOWNLOAD_URL" || true
  fi
  if [ -f "$BIN_PATH" ]; then
    chmod 755 "$BIN_PATH" || true
  fi
fi

if [ ! -x "$BIN_PATH" ] && [ -x "$FALLBACK_PATH" ]; then
  BIN_PATH="$FALLBACK_PATH"
fi

if [ ! -x "$BIN_PATH" ]; then
  echo "abc-node-probe binary not available" >&2
  exit 1
fi

exec "$BIN_PATH" "$@"
SCRIPT
				destination = "local/probe.sh"
				perms       = "0755"
			}
{{- end }}

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

The probe binary must be pre-installed at /opt/nomad/abc-node-probe on the target node.

  abc infra compute probe nomad-client-02 --jurisdiction=ZA`,
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

	// Resolve latest release asset for in-job download; fall back to pre-installed path.
	targetOS, targetArch := platformFromNode(node)
	downloadURL, version, err := utils.GetLatestReleaseAssetURLForPlatform(probeGitHubOwner, probeGitHubRepo, probeBinaryName, targetOS, targetArch)
	if err != nil {
		downloadURL = ""
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
	return nil
}

func platformFromNode(node *utils.NomadNode) (string, string) {
	if node == nil || node.Attributes == nil {
		return "linux", "amd64"
	}

	goos := strings.TrimSpace(node.Attributes["kernel.name"])
	if goos == "" {
		goos = strings.TrimSpace(node.Attributes["os.name"])
	}
	if goos == "" {
		goos = "linux"
	}

	goarch := strings.TrimSpace(node.Attributes["cpu.arch"])
	if goarch == "" {
		goarch = "amd64"
	}

	return goos, goarch
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

func buildNodeProbeJobHCL(datacenter, nodeID, probePath, downloadURL string, probeArgs []string) string {
	datacenter = strings.TrimSpace(datacenter)
	if datacenter == "" {
		datacenter = "dc1"
	}
	nodeID = strings.TrimSpace(nodeID)
	probePath = strings.TrimSpace(probePath)
	if probePath == "" {
		probePath = nodeProbeInstalledPath
	}
	command := probePath
	if strings.TrimSpace(downloadURL) != "" {
		command = "local/probe.sh"
	}

	argsHCL := "[]"
	if len(probeArgs) > 0 {
		if b, err := json.Marshal(probeArgs); err == nil {
			argsHCL = string(b)
		}
	}

	data := struct {
		JobID       string
		Datacenter  string
		NodeID      string
		Command     string
		ArgsHCL     string
		DownloadURL string
	}{
		JobID:       nodeProbeJobID,
		Datacenter:  datacenter,
		NodeID:      nodeID,
		Command:     command,
		ArgsHCL:     argsHCL,
		DownloadURL: strings.TrimSpace(downloadURL),
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
