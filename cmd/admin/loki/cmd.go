// Package loki provides the "abc admin services loki" subcommand group.
package loki

import (
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// NewCmd returns the "loki" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "loki",
		Short: "Loki log query helpers",
		Long: `Commands for querying Grafana Loki on an abc-nodes cluster.

  abc admin services loki query '{task="minio"}'
  abc admin services loki query '{stream="stderr"}' --since 2h --grep ERROR
  abc admin services loki cli -- query '{task="alloy"}' --limit 100   (logcli passthrough)`,
	}
	cmd.AddCommand(newQueryCmd())
	cmd.AddCommand(newCLICmd())
	return cmd
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <logql>",
		Short: "Query Loki with a LogQL expression and print matching log lines",
		Args:  cobra.ExactArgs(1),
		RunE:  runQuery,
	}
	cmd.Flags().String("since", "1h", "Show logs since this time: RFC3339 or duration (e.g. 2h, 30m)")
	cmd.Flags().String("until", "", "Show logs until this RFC3339 timestamp")
	cmd.Flags().String("grep", "", "Substring filter appended as |= to the LogQL expression")
	cmd.Flags().Int("limit", 500, "Maximum number of log lines to return")
	return cmd
}

func runQuery(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	lokiHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "loki", "http")
	if !ok || lokiHTTP == "" {
		return fmt.Errorf(
			"loki URL not configured for context %q\n"+
				"  Run: abc cluster capabilities sync",
			cfg.ActiveContext,
		)
	}

	logql := args[0]
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	grep, _ := cmd.Flags().GetString("grep")
	limit, _ := cmd.Flags().GetInt("limit")

	if grep != "" {
		logql += fmt.Sprintf(" |= %q", grep)
	}

	lc := floor.NewLokiClient(lokiHTTP)
	entries, err := lc.QueryRange(cmd.Context(), logql, since, until, limit)
	if err != nil {
		return fmt.Errorf("loki query: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "  No results for: %s\n", logql)
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(cmd.ErrOrStderr(), "  %d lines (query: %s)\n\n", len(entries), logql)
	for _, e := range entries {
		ts := e.Timestamp.Format("2006-01-02 15:04:05.000")
		task := e.Labels["task"]
		stream := e.Labels["stream"]
		parts := []string{ts}
		if task != "" {
			parts = append(parts, task+"/"+stream)
		}
		prefix := "[" + strings.Join(parts, " ") + "] "
		fmt.Fprintf(out, "%s%s\n", prefix, e.Line)
	}
	return nil
}

func newCLICmd() *cobra.Command {
	return &cobra.Command{
		Use:                "cli [logcli-args...]",
		Short:              "Run the local logcli binary (Loki CLI) with LOKI_ADDR pre-set",
		Long:               "Passthrough to the Grafana logcli binary. LOKI_ADDR is injected from admin.services.loki.http when not already set. Install logcli from https://github.com/grafana/loki/releases.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runCLI,
	}
}

func runCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_LOGCLI_BINARY", "LOGCLI_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		ctx := cfg.ActiveCtx()
		lokiHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "loki", "http")
		if ok && lokiHTTP != "" {
			base = utils.UpsertEnvOnlyMissing(base, map[string]string{"LOKI_ADDR": lokiHTTP})
		}
	}
	return utils.RunExternalCLIWithEnvAndBase(
		cmd.Context(), passthroughArgs, binaryLocation,
		[]string{"logcli"}, base, nil,
		os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(),
	)
}
