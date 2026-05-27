// runs.go — `abc report runs` subverb.
//
// Per-run cost + emissions table. Reads from local.db, no network.
// Default window: --since=30d, --limit=20. Jobs lead pipelines in the
// sort order; pipeline cost/CO₂e cells render "—" with a footnote
// (head-orchestrator-only resources would mislead by 10-100× — the
// cache aggregator that fixes this is on the Khan roadmap).
//
// Spec rationale: brainstorms/abc-report-use-cases/2026-05-27-… §R2.

package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	rep "github.com/abc-cluster/abc-cluster-cli/internal/report"
	"github.com/abc-cluster/abc-cluster-cli/internal/runner"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Per-run cost + emissions (jobs first; pipelines pending cache aggregator)",
		Long: `Show one row per submitted job/pipeline with its cost and emissions
estimate. Read entirely from ~/.abc/local.db — no network calls.

Sort order is jobs first (most recent first within), then pipelines.
The default window is the last 30 days and the default limit is 20
rows; widen with --since, --until, --limit. At thousands-of-rows
scale, prefer narrowing the window over raising the limit.

Pipeline rows render "—" for cost/CO₂e: the local DB carries the
pipeline-head orchestrator's resources only, which undercounts the
actual pipeline by 10-100×. Job rows are computed directly from the
single-alloc Nomad resources and are honest. The cache aggregator
that will populate pipeline cost across resumes is on the Khan
roadmap (brainstorm: brainstorms/abc-report-use-cases/2026-05-27-…).

Examples:
  abc report runs                       # last 30d, 20 rows, jobs first
  abc report runs --since=7d            # narrower window
  abc report runs --verb=job --limit=50 # jobs only, more rows
  abc report runs --json                # machine-readable
`,
		RunE: runReportRuns,
	}
	cmd.Flags().String("since", "", "window start (YYYY-MM-DD, or relative like 7d / 24h); default 30 days ago")
	cmd.Flags().String("until", "", "window end (YYYY-MM-DD); default now")
	cmd.Flags().Int("limit", 20, "max rows to return")
	cmd.Flags().String("verb", "", "filter: job | pipeline (default: both)")
	cmd.Flags().Bool("json", false, "emit machine-readable JSON")
	cmd.Flags().Bool("full", false, "per-row vertical block layout with every forensic field (run_id, nomad_job_id, exit_code, exit_reason, namespace, project, investigation, workdir_root, params); useful for tracing")
	cmd.Flags().Bool("no-reconcile", false, "skip the Nomad re-probe for runs whose watcher goroutine died (faster, but rows may stay 'running')")
	return cmd
}

func runReportRuns(cmd *cobra.Command, _ []string) error {
	since, until, err := resolveRunsWindow(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	verb := strings.ToLower(strings.TrimSpace(mustGetString(cmd, "verb")))
	if verb != "" && verb != "job" && verb != "pipeline" {
		return fmt.Errorf("--verb must be one of: job, pipeline (got %q)", verb)
	}
	jsonOut, _ := cmd.Flags().GetBool("json")

	contextName := state.ActiveContextName()
	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local state: %w", err)
	}
	defer db.Close()

	// Re-probe Nomad for runs whose watcher goroutine died (the CLI
	// exits seconds after `abc job run` returns, but Nomad jobs take
	// minutes to complete — without this step every row appears as
	// 'running' forever). ReconcileStuckRuns short-circuits cleanly
	// if Nomad is unreachable.
	if !boolFlag(cmd, "no-reconcile") {
		nomadAddr, nomadToken, region, fallbackNS := activeContextNomad(contextName)
		if nomadAddr != "" {
			n := runner.ReconcileStuckRuns(cmd.Context(), nomadAddr, nomadToken, region, 30*time.Second, cmd.ErrOrStderr(), fallbackNS)
			if n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "[abc] reconciled %d stuck run(s) from Nomad\n", n)
			}
		}
	}

	card, err := rep.LoadRateCard(contextName)
	if err != nil {
		return err
	}

	res, err := rep.QueryRuns(cmd.Context(), db, rep.RunsQuery{
		ContextName: contextName,
		Since:       since,
		Until:       until,
		Verb:        verb,
		Limit:       limit,
	}, card)
	if err != nil {
		return fmt.Errorf("query runs: %w", err)
	}

	if jsonOut {
		return emitRunsJSON(cmd, res, since, until, verb)
	}
	currSym := "R"
	if c := strings.TrimSpace(card.Currency.Value); c != "" && c != "ZAR" {
		currSym = c
	}
	return rep.RenderRunsText(cmd.OutOrStdout(), res.Rows, rep.RunsTextOptions{
		CurrencySymbol: currSym,
		Total:          res.Total,
		Since:          since,
		Until:          until,
		Verb:           verb,
		Full:           boolFlag(cmd, "full"),
	})
}

func emitRunsJSON(cmd *cobra.Command, res rep.RunsResult, since, until time.Time, verb string) error {
	type row struct {
		RunID           string    `json:"run_id"`
		Verb            string    `json:"verb"`
		WorkloadRef     string    `json:"workload_ref"`
		WorkloadVersion string    `json:"workload_version,omitempty"`
		Status          string    `json:"status"`
		SubmittedAt     time.Time `json:"submitted_at"`
		CompletedAt     *time.Time `json:"completed_at,omitempty"`
		CPUHours        float64   `json:"cpu_hours"`
		MemGBHours      float64   `json:"memory_gb_hours"`
		WalltimeSec     int64     `json:"walltime_seconds"`
		CostZAR         *float64  `json:"cost_zar,omitempty"`
		EmissionsKgCO2e *float64  `json:"emissions_kg_co2e,omitempty"`
		CostPending     bool      `json:"cost_pending"`
		// Forensic / tracing fields (always emitted in JSON; the
		// columnar text view hides them, the --full text view shows
		// them, but a JSON consumer always gets the same shape).
		ExitReason      string    `json:"exit_reason,omitempty"`
		ExitCode        int64     `json:"exit_code,omitempty"`
		NomadJobID      string    `json:"nomad_job_id,omitempty"`
		Namespace       string    `json:"namespace,omitempty"`
		ProjectID       string    `json:"project_id,omitempty"`
		InvestigationID string    `json:"investigation_id,omitempty"`
		FreezeID        string    `json:"freeze_id,omitempty"`
		WorkdirRoot     string    `json:"workdir_root,omitempty"`
		ParamsJSON      string    `json:"params_json,omitempty"`
	}
	out := struct {
		Window struct {
			Since time.Time `json:"since"`
			Until time.Time `json:"until,omitempty"`
		} `json:"window"`
		Verb  string `json:"verb,omitempty"`
		Total int    `json:"total"`
		Rows  []row  `json:"rows"`
	}{}
	out.Window.Since = since
	out.Window.Until = until
	out.Verb = verb
	out.Total = res.Total
	out.Rows = make([]row, 0, len(res.Rows))
	for _, r := range res.Rows {
		jr := row{
			RunID:           r.RunID,
			Verb:            r.Verb,
			WorkloadRef:     r.WorkloadRef,
			WorkloadVersion: r.WorkloadVersion,
			Status:          r.Status,
			SubmittedAt:     r.SubmittedAt,
			CompletedAt:     r.CompletedAt,
			CPUHours:        r.CPUHours,
			MemGBHours:      r.MemGBHours,
			WalltimeSec:     r.WalltimeSec,
			CostPending:     r.CostPending,
			ExitReason:      r.ExitReason,
			ExitCode:        r.ExitCode,
			NomadJobID:      r.NomadJobID,
			Namespace:       r.Namespace,
			ProjectID:       r.ProjectID,
			InvestigationID: r.InvestigationID,
			FreezeID:        r.FreezeID,
			WorkdirRoot:     r.WorkdirRoot,
			ParamsJSON:      r.ParamsJSON,
		}
		// Only emit numeric cost/em values when they're meaningful.
		// Pipeline rows leave the keys absent (omitempty on *float64)
		// to match the text "—" rendering — clients see the absence
		// and the cost_pending=true flag instead of a misleading zero.
		if !r.CostPending {
			c := r.CostZAR
			e := r.EmissionsKgCO2e
			jr.CostZAR = &c
			jr.EmissionsKgCO2e = &e
		}
		out.Rows = append(out.Rows, jr)
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// resolveRunsWindow parses --since / --until. Accepts both absolute
// dates (YYYY-MM-DD) and relative durations (7d, 24h). Default since
// = 30 days ago, default until = now.
func resolveRunsWindow(cmd *cobra.Command) (since, until time.Time, err error) {
	now := time.Now().UTC()
	sinceFlag := mustGetString(cmd, "since")
	untilFlag := mustGetString(cmd, "until")

	if s := strings.TrimSpace(sinceFlag); s == "" {
		since = now.AddDate(0, 0, -30)
	} else if d, perr := parseRelativeDuration(s); perr == nil {
		since = now.Add(-d)
	} else if t, perr := time.Parse("2006-01-02", s); perr == nil {
		since = t.UTC()
	} else {
		return time.Time{}, time.Time{}, fmt.Errorf("--since %q: expected YYYY-MM-DD or a duration like 7d / 24h", s)
	}

	if s := strings.TrimSpace(untilFlag); s != "" {
		t, perr := time.Parse("2006-01-02", s)
		if perr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--until %q: expected YYYY-MM-DD", s)
		}
		until = t.UTC()
	} else {
		until = now
	}
	return since, until, nil
}

// parseRelativeDuration accepts "7d", "24h", "30m" — Go's time.Parse
// doesn't natively know "d", so we expand days into hours before
// handing off to time.ParseDuration.
func parseRelativeDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func mustGetString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// activeContextNomad returns (addr, token, region, namespace) for the
// active context's Nomad service block. Returns "" addr if no context
// or no Nomad service — caller treats empty as "skip reconcile."
//
// Namespace is the fallback for reconcile when runs.namespace is blank
// (historical rows whose `abc job run` invocation didn't capture the
// namespace at submit time).
func activeContextNomad(contextName string) (addr, token, region, namespace string) {
	c, err := cfg.Load()
	if err != nil {
		return "", "", "", ""
	}
	ctx, ok := c.ContextNamed(contextName)
	if !ok {
		return "", "", "", ""
	}
	return ctx.NomadAddr(), ctx.NomadToken(), ctx.NomadRegion(), ctx.NomadNamespace()
}

// (boolFlag is defined in cmd/report/report.go and shared across subverbs.)

// ensure unused-import suppression isn't needed; context is referenced
// through the cobra command.
var _ = context.Background
