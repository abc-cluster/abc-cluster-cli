// Package report wires the `abc report` verb — researcher-productivity
// summary read entirely from ~/.abc/local.db. Spec abc-report.md.
//
// No network calls in any code path. The verb declares
// `Required{ AllOf: [{Service: "local-state"}] }` and `OptionalFor:
// {"all-contexts": {Service: "abc-controller-svc"}}`; --all-contexts
// rejects with the standard capability.Require() failure message.
package report

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/capability"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	rep "github.com/abc-cluster/abc-cluster-cli/internal/report"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// reportCapabilities mirrors the shape `abc accounting` uses. Local
// state is always sufficient at seed; --all-contexts is gated by the
// controller service (Phase 2).
var reportCapabilities = capability.Required{
	AllOf: []capability.Need{
		{Service: "local-state", MinVersion: "0001_initial",
			Features: []string{"runs"}},
	},
	OptionalFor: map[string]capability.Need{
		"all-contexts": {Service: "abc-controller-svc", MinVersion: "0.1.0",
			Features: []string{"federation-aggregate"}},
	},
}

// NewCmd returns the top-level `abc report` command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Researcher productivity report — read locally from ~/.abc/local.db",
		Long: `Print a personal productivity summary: investigations, runs,
compute, and an estimate of how much accidental time the platform
returned to you. No network calls — everything is read from
~/.abc/local.db. See docs/reference/abc-report.md for the metric
mapping table and JSON schema.`,
		RunE: runReport,
	}
	cmd.Flags().String("since", "", "report window start (YYYY-MM-DD); default Jan 1 of current year")
	cmd.Flags().String("until", "", "report window end (YYYY-MM-DD); default now")
	cmd.Flags().String("by", "", "aggregation axis: investigation|pipeline|project|user (JSON only in v1)")
	cmd.Flags().Bool("json", false, "emit machine-readable JSON; metric IDs are keys")
	cmd.Flags().Bool("technical", false, "use metric IDs instead of human titles in text output")
	cmd.Flags().Bool("show-rate-card", false, "include the detailed per-rate provenance block + override hints (default hidden)")
	cmd.Flags().Bool("all-contexts", false, "(Phase 2 — currently rejects with a clear error)")
	// Subverbs.
	cmd.AddCommand(newRunsCmd())
	cmd.AddCommand(newComplianceCmd())
	return cmd
}

func runReport(cmd *cobra.Command, _ []string) error {
	caps := loadActiveContextCapabilities()
	flagsSet := map[string]bool{
		"all-contexts": boolFlag(cmd, "all-contexts"),
	}
	decision := capability.Require(reportCapabilities, caps, flagsSet)
	if decision.Failed() {
		return decision.AsError()
	}

	since, until, err := resolveWindow(cmd)
	if err != nil {
		return err
	}
	window := rep.Window{Since: since, Until: until}

	jsonOut, _ := cmd.Flags().GetBool("json")
	technical, _ := cmd.Flags().GetBool("technical")
	showRateCard, _ := cmd.Flags().GetBool("show-rate-card")
	by, _ := cmd.Flags().GetString("by")

	// v1 grouping policy (per-spec clarification): JSON mode supports
	// --by=<axis> by emitting a top-level groups[] array; text mode
	// defers to v2 with a clear error so the v1 surface stays honest.
	if by != "" && !jsonOut {
		return fmt.Errorf("--by=<axis> grouped output is deferred to v2; --json --by=<axis> is supported in v1 (groups returned as a top-level array)")
	}

	contextName := state.ActiveContextName()
	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local state: %w", err)
	}
	defer db.Close()

	// Layer-0/1 rate card resolved exactly as `abc accounting` and
	// `abc emissions` do. Spec §D' (BINDING): one resolver across the
	// three verbs is the contract; drift is a regression.
	card, err := rep.LoadRateCard(contextName)
	if err != nil {
		return err
	}

	opts := rep.QueryOptions{Window: window, ContextName: contextName, RateCard: card}
	results := rep.Compute(cmd.Context(), db, opts)

	if jsonOut {
		var groups []rep.JSONGroup
		if by != "" {
			groups, err = computeGroups(cmd.Context(), db, opts, by)
			if err != nil {
				return err
			}
		}
		return rep.RenderJSON(cmd.OutOrStdout(), window, contextName, card, results, groups)
	}

	textOpts := rep.TextOptions{Window: window, Technical: technical, ShowRateCard: showRateCard, ContextName: contextName, RateCard: card}
	return rep.RenderText(cmd.Context(), db, cmd.OutOrStdout(), textOpts, results)
}

// computeGroups produces a top-level "groups" array for JSON output
// when --by=<axis> is set. The list of group keys for the chosen axis
// is sourced from runs in the window; per-group metric values are
// computed by re-running the metric pipeline against a row-filtered
// view of the same DB. This is JSON-only in v1 (text mode rejects
// --by= upstream).
//
// Group axes today:
//   - investigation: COALESCE(investigation_id, '(none)')
//   - project:       COALESCE(project_id, '(none)')
//   - pipeline:      workload_ref
//   - user:          context_name (closest proxy at seed; per-user
//                    attribution lands when abc-cloud's identity
//                    layer is wired)
func computeGroups(ctx context.Context, db *sql.DB, opts rep.QueryOptions, by string) ([]rep.JSONGroup, error) {
	column, ok := groupColumnFor(by)
	if !ok {
		return nil, fmt.Errorf("--by=%s: unsupported axis (allowed: investigation|project|pipeline|user)", by)
	}
	q := fmt.Sprintf(`SELECT DISTINCT COALESCE(%s, '(none)') FROM runs
	                  WHERE submitted_at >= ? AND submitted_at <= ? AND context_name = ?
	                  ORDER BY 1`, column)
	rows, err := db.QueryContext(ctx, q, opts.Window.Since.Unix(), opts.Window.Until.Unix(), opts.ContextName)
	if err != nil {
		return nil, fmt.Errorf("group keys: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	out := make([]rep.JSONGroup, 0, len(keys))
	for _, k := range keys {
		groupOpts := opts
		groupOpts.GroupColumn = column
		groupOpts.GroupKey = k
		// Re-run Compute with the per-group filter applied. The query
		// helpers honour GroupColumn/GroupKey when set (added in the
		// same commit that adds this verb).
		results := rep.Compute(ctx, db, groupOpts)
		out = append(out, rep.JSONGroup{
			By:      by,
			Key:     k,
			Metrics: jsonMetricMapFromResults(results),
		})
	}
	return out, nil
}

func groupColumnFor(by string) (string, bool) {
	switch by {
	case "investigation":
		return "investigation_id", true
	case "project":
		return "project_id", true
	case "pipeline":
		return "workload_ref", true
	case "user":
		// Closest proxy until per-user attribution lands.
		return "context_name", true
	}
	return "", false
}

// jsonMetricMapFromResults rebuilds a JSONMetric map from a flat
// MetricResult slice. Mirrors the unexported helper inside the report
// package so callers don't need to reach in.
func jsonMetricMapFromResults(rs []rep.MetricResult) map[string]rep.JSONMetric {
	out := make(map[string]rep.JSONMetric, len(rs))
	for _, r := range rs {
		label := rep.MetricByID(r.ID)
		out[r.ID] = rep.JSONMetric{
			ID:         r.ID,
			Label:      label.Title,
			Gloss:      label.Gloss,
			Unit:       label.Unit,
			Value:      r.Value,
			Computable: r.Computable,
			Reason:     r.Reason,
		}
	}
	return out
}

// resolveWindow applies the same precedence the rest of the CLI uses:
// --since/--until > defaults. Default is YTD anchored at Jan 1 of the
// current year, in UTC.
func resolveWindow(cmd *cobra.Command) (time.Time, time.Time, error) {
	const layout = "2006-01-02"
	def := rep.DefaultWindowYTD(time.Now())
	since := def.Since
	until := def.Until
	if v, _ := cmd.Flags().GetString("since"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			return since, until, fmt.Errorf("--since: %w", err)
		}
		since = t
	}
	if v, _ := cmd.Flags().GetString("until"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			return since, until, fmt.Errorf("--until: %w", err)
		}
		until = t.Add(24 * time.Hour) // inclusive end-of-day
	}
	return since, until, nil
}

// loadActiveContextCapabilities returns the active context's
// Capabilities map. Mirrors the helper in cmd/accounting and
// cmd/emissions; nil result is fine — capability.Require treats it as
// "Services map empty; local-state available via fallback".
func loadActiveContextCapabilities() *config.Capabilities {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return nil
	}
	return cfg.Contexts[ctxName].Capabilities
}

func boolFlag(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	v, _ := cmd.Flags().GetBool(name)
	return v
}
