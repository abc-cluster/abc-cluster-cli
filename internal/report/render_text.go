package report

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"
)

// HourlyRateZAR is the postdoc hourly rate (R/hr) used to translate
// `hours_saved` into a currency amount in the headline footer. Spec
// abc-report.md §"Acceptance: end-to-end" sample. Sourced from the
// HSRC 2025 South African postdoctoral compensation guidance via the
// Layer-0 ZA defaults.
const HourlyRateZAR = 350

// PostdocRateProvenance is the citation string surfaced in the footer.
// Mirrors the shape `abc accounting` uses ("Rate card: …").
const PostdocRateProvenance = "Rate card: Layer-0 ZA defaults (postdoc R350/hr, citation: HSRC 2025)."

// TextOptions tunes the text renderer.
type TextOptions struct {
	// Technical replaces user-facing Title with metric ID in the
	// section labels — useful for documentation snippets and power
	// users. Glosses are unchanged.
	Technical bool

	// Window is rendered in the header.
	Window Window

	// ContextName is the active context the summary is scoped to. Must
	// match the ContextName passed to QueryOptions so the headline
	// breakdown and metric pipeline see the same row set.
	ContextName string
}

// RenderText writes the §5.10 mockup-style summary to w. Reads the
// metric results, formats the headline blocks (questions / runs /
// compute / time-saved), and prints the postdoc-rate translation +
// provenance footer.
//
// The renderer pulls supplementary aggregates (investigation count,
// total compute) directly from the DB rather than threading every
// number through the metric framework — those are summary-only and
// don't carry IDs.
func RenderText(ctx context.Context, db *sql.DB, w io.Writer, opts TextOptions, results []MetricResult) error {
	yearLabel := opts.Window.Since.Format("2006")
	if opts.Window.Since.Year() != opts.Window.Until.Year() {
		yearLabel = fmt.Sprintf("%s–%s", opts.Window.Since.Format("2006-01-02"), opts.Window.Until.Format("2006-01-02"))
	}

	investigations, runsTotal, runsSucceeded, runsRetried, cpuHours, gpuHours, err := summaryAggregates(ctx, db, opts.Window, opts.ContextName)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Your %s so far:\n", yearLabel)
	fmt.Fprintln(w, strings.Repeat("─", 52))
	fmt.Fprintf(w, "%-37s %d\n", labelOrTech(opts, "Questions explored (investigations):", "investigations_count"), investigations)
	fmt.Fprintf(w, "%-37s %d  (%d worked, %d retried)\n",
		labelOrTech(opts, "Pipeline runs:", "runs_count"), runsTotal, runsSucceeded, runsRetried)
	fmt.Fprintf(w, "%-37s %.0f CPU-hrs, %.0f GPU-hrs\n",
		labelOrTech(opts, "Total compute:", "compute_hours"), cpuHours, gpuHours)
	fmt.Fprintln(w)

	// Time-saved breakdown.
	fmt.Fprintln(w, "Research time saved (estimated):")
	rows := timeSavedRows(ctx, db, opts.Window, opts.ContextName)
	maxLabel := 0
	for _, r := range rows {
		if n := len(r.label); n > maxLabel {
			maxLabel = n
		}
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s →  %s\n", maxLabel, r.label, r.value)
	}
	fmt.Fprintln(w, "  "+strings.Repeat("─", 50))
	hoursSaved, _ := ResultByID(results, "hours_saved")
	totalStr := "n/a"
	if hoursSaved.Computable {
		if v, ok := hoursSaved.Value.(float64); ok {
			totalStr = fmt.Sprintf("~%.1f hrs", v)
		}
	}
	fmt.Fprintf(w, "  %-*s    %s\n", maxLabel, "Total:", totalStr)
	fmt.Fprintln(w)

	// Currency translation.
	hours := 0.0
	if hoursSaved.Computable {
		if v, ok := hoursSaved.Value.(float64); ok {
			hours = v
		}
	}
	rateZAR := HourlyRateZAR
	amount := hours * float64(rateZAR)
	fmt.Fprintf(w, "%-23s %.1f hours\n", labelOrTech(opts, "Research time saved:", "hours_saved"), hours)
	fmt.Fprintf(w, "%-23s R %d\n", labelOrTech(opts, "Hourly compensation:", "hourly_rate_zar"), rateZAR)
	fmt.Fprintf(w, "%-23s R %s\n", labelOrTech(opts, "Amount:", "amount_zar"), formatThousands(amount))
	fmt.Fprintln(w)
	fmt.Fprintln(w, PostdocRateProvenance)
	return nil
}

// labelOrTech returns the technical key when --technical, otherwise the
// human label. Mirrors the toggle for metric Titles in the structured
// metrics block.
func labelOrTech(opts TextOptions, human, tech string) string {
	if opts.Technical {
		return tech
	}
	return human
}

type tsRow struct {
	label string
	value string
}

// timeSavedRows builds the per-heuristic breakdown lines. Each row's
// "value" is either a "~X.Y hrs" string or "n/a (reason)" if the
// underlying counter isn't computable.
func timeSavedRows(ctx context.Context, db *sql.DB, w Window, contextName string) []tsRow {
	out := []tsRow{}
	whereRuns, argsRuns := runWindowClause(QueryOptions{Window: w, ContextName: contextName})

	// Auto-retry → stabilisation_runs as the proxy.
	var stab int
	_ = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs WHERE "+whereRuns+" AND status='failed' AND workload_ref IN (SELECT workload_ref FROM runs WHERE "+whereRuns+" AND status='completed')",
		append(argsRuns, argsRuns...)...,
	).Scan(&stab)
	out = append(out, tsRow{"Auto-retry handled it for you",
		fmt.Sprintf("~%.1f hrs", float64(stab*AutoRetrySavedMinutes)/60.0)})

	// Smart resource defaults — depends on cpu_request column.
	var smart sql.NullInt64
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs WHERE "+whereRuns+" AND cpu_request IS NOT NULL", argsRuns...,
	).Scan(&smart)
	switch {
	case err != nil && isMissingColumnErr(err):
		out = append(out, tsRow{"Smart resource defaults accepted",
			"n/a (" + MissingColumnReason("0009_runs_resource_request") + ")"})
	case err != nil || !smart.Valid || smart.Int64 == 0:
		out = append(out, tsRow{"Smart resource defaults accepted",
			"n/a (requires migration 0009 data)"})
	default:
		out = append(out, tsRow{"Smart resource defaults accepted",
			fmt.Sprintf("~%.1f hrs", float64(smart.Int64*SmartDefaultSavedMinutes)/60.0)})
	}

	// Failure summaries.
	var failed int
	_ = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs WHERE "+whereRuns+" AND status='failed'", argsRuns...,
	).Scan(&failed)
	out = append(out, tsRow{"Failure summaries (vs. log diving)",
		fmt.Sprintf("~%.1f hrs", float64(failed*FailureSummarySavedMinutes)/60.0)})

	// Templates / reruns.
	var templated sql.NullInt64
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs WHERE "+whereRuns+" AND (submission_source LIKE 'template:%' OR submission_source='rerun')", argsRuns...,
	).Scan(&templated)
	switch {
	case err != nil && isMissingColumnErr(err):
		out = append(out, tsRow{"Reused protocols (vs. from scratch)",
			"n/a (" + MissingColumnReason("0010_runs_submission_source") + ")"})
	case err != nil:
		out = append(out, tsRow{"Reused protocols (vs. from scratch)", "n/a (" + err.Error() + ")"})
	default:
		out = append(out, tsRow{"Reused protocols (vs. from scratch)",
			fmt.Sprintf("~%.1f hrs", float64(templated.Int64*TemplateReuseSavedMinutes)/60.0)})
	}

	return out
}

// summaryAggregates returns the headline numbers that sit above the
// time-saved block. Pulled directly from runs/investigations rather than
// going through the metric framework (these are summary, not metrics).
func summaryAggregates(ctx context.Context, db *sql.DB, w Window, contextName string) (
	investigations int,
	runsTotal int, runsSucceeded int, runsRetried int,
	cpuHours float64, gpuHours float64,
	err error,
) {
	whereRuns, argsRuns := runWindowClause(QueryOptions{Window: w, ContextName: contextName})

	if err = db.QueryRowContext(ctx,
		"SELECT COUNT(DISTINCT investigation_id) FROM runs WHERE "+whereRuns+" AND investigation_id IS NOT NULL",
		argsRuns...,
	).Scan(&investigations); err != nil {
		return
	}

	row := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),
			COALESCE(SUM(cpu_hours), 0),
			COALESCE(SUM(CASE WHEN gpu_count > 0 THEN cpu_hours ELSE 0 END), 0)
		FROM runs WHERE `+whereRuns, argsRuns...)
	var s, f sql.NullInt64
	if err = row.Scan(&runsTotal, &s, &f, &cpuHours, &gpuHours); err != nil {
		return
	}
	if s.Valid {
		runsSucceeded = int(s.Int64)
	}
	if f.Valid {
		runsRetried = int(f.Int64)
	}
	return
}

// formatThousands renders a non-negative float as an integer with comma
// thousands separators, matching the §"Acceptance" sample (R 1,225).
func formatThousands(v float64) string {
	if v < 0 {
		return "-" + formatThousands(-v)
	}
	n := int64(v + 0.5)
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	out := []byte{}
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// DefaultWindowYTD returns the YTD window used when --since/--until are
// unset. now is the upper bound; lower bound is Jan 1 of the current
// year. UTC boundaries.
func DefaultWindowYTD(now time.Time) Window {
	jan1 := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	return Window{Since: jan1, Until: now}
}
