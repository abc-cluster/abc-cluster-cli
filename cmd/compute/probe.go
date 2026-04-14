package compute

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

const (
	defaultNodeProbeBinaryPath = "/Users/abhi/projects/PHD-pub-abc-cluster/analysis/packages/abc-node-probe/abc-node-probe"
	nodeProbeJobID             = "abc-node-probe-system"
	defaultProbeWaitTimeout    = 5 * time.Minute
	defaultProbeWatchDelay     = 2 * time.Second
)

var nodeProbeJobTemplate = template.Must(template.New("node_probe_job").Parse(`job {{printf "%q" .JobID}} {
	type        = "system"
	datacenters = [{{printf "%q" .Datacenter}}]

	parameterized {
		payload       = "forbidden"
		meta_required = ["node_id"]
		meta_optional = ["jurisdiction", "skip_categories", "json_only", "fail_fast"]
	}

	group "probe" {
		constraint {
			attribute = "${node.unique.id}"
			operator  = "="
			value     = "${meta.node_id}"
		}

		restart {
			attempts = 0
			mode     = "fail"
		}

		task "probe" {
			driver = "raw_exec"

			config {
				command = "/bin/sh"
				args    = ["-lc", {{printf "%q" .Script}}]
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
		Short: "Run abc-node-probe on a specific node via a parameterized system job",
		Long: `Deploy and execute the abc-node-probe binary on a specific Nomad node.

This command registers a system-level parameterized Nomad job, dispatches it
for one node, and streams probe output.

  abc infra compute probe nomad-client-02 --sudo --jurisdiction=ZA`,
		Args: cobra.ExactArgs(1),
		RunE: runProbe,
	}
	cmd.Flags().String("binary", defaultNodeProbeBinaryPath,
		"Path to abc-node-probe Linux binary to deploy")
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

	binaryPath, _ := cmd.Flags().GetString("binary")
	binaryBytes, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("reading probe binary %q: %w", binaryPath, err)
	}
	if len(binaryBytes) == 0 {
		return fmt.Errorf("probe binary %q is empty", binaryPath)
	}

	probeHCL := buildNodeProbeJobHCL(node.Datacenter, binaryBytes)
	jobJSON, err := nc.ParseHCL(cmd.Context(), probeHCL)
	if err != nil {
		return fmt.Errorf("nomad HCL parse for %q: %w", nodeProbeJobID, err)
	}

	if _, err := nc.RegisterJob(cmd.Context(), jobJSON); err != nil {
		return fmt.Errorf("registering probe job %q: %w", nodeProbeJobID, err)
	}

	meta := map[string]string{"node_id": node.ID}
	if v, _ := cmd.Flags().GetString("jurisdiction"); strings.TrimSpace(v) != "" {
		meta["jurisdiction"] = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetString("skip-categories"); strings.TrimSpace(v) != "" {
		meta["skip_categories"] = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetBool("json"); v {
		meta["json_only"] = "1"
	}
	if v, _ := cmd.Flags().GetBool("fail-fast"); v {
		meta["fail_fast"] = "1"
	}

	resp, err := nc.DispatchJob(cmd.Context(), nodeProbeJobID, meta, nil)
	if err != nil {
		return fmt.Errorf("dispatching probe job for node %q: %w", node.ID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  ✓ Probe dispatched\n")
	fmt.Fprintf(out, "  Node           %s (%s)\n", node.Name, shortID(node.ID))
	fmt.Fprintf(out, "  Nomad job ID   %s\n", resp.DispatchedJobID)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)

	detach, _ := cmd.Flags().GetBool("detach")
	if detach {
		return nil
	}

	waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")
	fmt.Fprintf(out, "\n  Streaming probe output...\n\n")
	if err := utils.WatchJobLogs(cmd.Context(), nc, resp.DispatchedJobID, "", out, defaultProbeWatchDelay, waitTimeout); err != nil {
		return fmt.Errorf("streaming probe output: %w", err)
	}
	return nil
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

func buildNodeProbeJobHCL(datacenter string, binary []byte) string {
	datacenter = strings.TrimSpace(datacenter)
	if datacenter == "" {
		datacenter = "dc1"
	}

	encoded := wrapAt(base64.StdEncoding.EncodeToString(binary), 120)

	script := strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"cat > \"${NOMAD_TASK_DIR}/abc-node-probe.b64\" <<'B64'",
		encoded,
		"B64",
		"base64 -d \"${NOMAD_TASK_DIR}/abc-node-probe.b64\" > \"${NOMAD_TASK_DIR}/abc-node-probe\"",
		"chmod 0755 \"${NOMAD_TASK_DIR}/abc-node-probe\"",
		"ARGS=\"--mode=stdout\"",
		"if [ -n \"${NOMAD_META_jurisdiction:-}\" ]; then ARGS=\"$ARGS --jurisdiction=${NOMAD_META_jurisdiction}\"; fi",
		"if [ -n \"${NOMAD_META_skip_categories:-}\" ]; then ARGS=\"$ARGS --skip-categories=${NOMAD_META_skip_categories}\"; fi",
		"if [ \"${NOMAD_META_json_only:-}\" = \"1\" ]; then ARGS=\"$ARGS --json\"; fi",
		"if [ \"${NOMAD_META_fail_fast:-}\" = \"1\" ]; then ARGS=\"$ARGS --fail-fast\"; fi",
		"# shellcheck disable=SC2086",
		"\"${NOMAD_TASK_DIR}/abc-node-probe\" $ARGS",
	}, "\n")

	data := struct {
		JobID      string
		Datacenter string
		Script     string
	}{
		JobID:      nodeProbeJobID,
		Datacenter: datacenter,
		Script:     script,
	}

	var b bytes.Buffer
	if err := nodeProbeJobTemplate.Execute(&b, data); err != nil {
		// Template is statically validated with Must; keep a defensive fallback.
		panic(err)
	}
	return b.String()
}

func wrapAt(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}

	var b strings.Builder
	for len(s) > width {
		b.WriteString(s[:width])
		b.WriteByte('\n')
		s = s[width:]
	}
	b.WriteString(s)
	return b.String()
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
