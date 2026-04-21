// Package prometheus provides the "abc admin services prometheus" subcommand group.
package prometheus

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// NewCmd returns the "prometheus" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prometheus",
		Short: "Prometheus metric query helpers",
		Long: `Commands for querying Prometheus on an abc-nodes cluster.

  abc admin services prometheus query 'nomad_client_allocated_cpu'
  abc admin services prometheus query 'nomad_nomad_job_summary_running{namespace="default"}'
  abc admin services prometheus cli -- query instant --query 'up'   (promtool passthrough)`,
	}
	cmd.AddCommand(newQueryCmd())
	cmd.AddCommand(newCLICmd())
	return cmd
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <promql>",
		Short: "Execute an instant PromQL query and print the result vector",
		Args:  cobra.ExactArgs(1),
		RunE:  runQuery,
	}
	cmd.Flags().Bool("labels", false, "Show full label set for each result")
	return cmd
}

func runQuery(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	promHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "prometheus", "http")
	if !ok || promHTTP == "" {
		return fmt.Errorf(
			"prometheus URL not configured for context %q\n"+
				"  Run: abc cluster capabilities sync",
			cfg.ActiveContext,
		)
	}

	promql := args[0]
	showLabels, _ := cmd.Flags().GetBool("labels")

	pc := floor.NewPrometheusClient(promHTTP)
	metrics, err := pc.Query(cmd.Context(), promql)
	if err != nil {
		return fmt.Errorf("prometheus query: %w", err)
	}

	if len(metrics) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "  No results for: %s\n", promql)
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(cmd.ErrOrStderr(), "  %d result(s)\n\n", len(metrics))

	for _, m := range metrics {
		if showLabels {
			// Sort label keys for stable output.
			keys := make([]string, 0, len(m.Labels))
			for k := range m.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				if k == "__name__" {
					continue
				}
				parts = append(parts, fmt.Sprintf("%s=%q", k, m.Labels[k]))
			}
			label := "{" + strings.Join(parts, ", ") + "}"
			fmt.Fprintf(out, "  %g %s\n", m.Value, label)
		} else {
			// Print metric name + value; omit internal __name__ label.
			name := m.Labels["__name__"]
			if name == "" {
				name = promql
			}
			// Include the most distinctive label (host, exported_job, task…) if present.
			var suffix string
			for _, key := range []string{"host", "exported_job", "task", "instance", "job"} {
				if v, ok := m.Labels[key]; ok && v != "" {
					suffix = " {" + key + "=" + v + "}"
					break
				}
			}
			fmt.Fprintf(out, "  %-50s %g\n", name+suffix, m.Value)
		}
	}
	return nil
}

func newCLICmd() *cobra.Command {
	return &cobra.Command{
		Use:                "cli [promtool-args...]",
		Short:              "Run the local promtool binary with PROMETHEUS_URL pre-set",
		Long:               "Passthrough to the Prometheus promtool binary. Optional leading `--config local|nomad|vault` (default local) resolves PROMETHEUS_URL from admin.services.prometheus (cred_source + top-level). Install promtool from https://prometheus.io/download/.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runCLI,
	}
}

func runCLI(cmd *cobra.Command, args []string) error {
	configSelection, binaryLocation, passthroughArgs, err := utils.ParseAdminServiceCLIArgs(args, true)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_PROMTOOL_BINARY", "PROMTOOL_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		ctx := cfg.ActiveCtx()
		svc := config.AdminFloorServiceNamed(&ctx.Admin.Services, "prometheus")
		promHTTP, rerr := utils.ResolveAdminFloorField(cmd.Context(), ctx, svc, "prometheus", configSelection, "http")
		if rerr != nil {
			return rerr
		}
		if promHTTP != "" {
			base = utils.UpsertEnvOnlyMissing(base, map[string]string{"PROMETHEUS_URL": promHTTP})
		}
	}
	return utils.RunExternalCLIWithEnvAndBase(
		cmd.Context(), passthroughArgs, binaryLocation,
		[]string{"promtool"}, base, nil,
		os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(),
	)
}
