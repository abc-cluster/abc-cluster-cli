package report

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

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

	// RateCard is the resolved Layer-0/1 rate card (and grid intensity).
	// Used to print the postdoc compensation line and the unified
	// provenance footer. When zero, the renderer falls back to the
	// Layer-0 ZA defaults so unit tests stay deterministic.
	RateCard acct.RateCard
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

	card := resolvedTextCard(opts)
	currency := card.Currency.Value
	if currency == "" {
		currency = "ZAR"
	}
	currencySym := currencySymbol(currency)

	fmt.Fprintf(w, "Your %s so far:\n", yearLabel)
	fmt.Fprintln(w, strings.Repeat("─", 52))
	fmt.Fprintf(w, "%-37s %d\n", labelOrTech(opts, "Questions explored (investigations):", "investigations_count"), investigations)
	fmt.Fprintf(w, "%-37s %d  (%d worked, %d retried)\n",
		labelOrTech(opts, "Pipeline runs:", "runs_count"), runsTotal, runsSucceeded, runsRetried)
	fmt.Fprintf(w, "%-37s %.0f CPU-hrs, %.0f GPU-hrs\n",
		labelOrTech(opts, "Total compute:", "compute_hours"), cpuHours, gpuHours)
	fmt.Fprintln(w)

	// §D' BINDING: spend + emissions headline, sandwiched between the
	// compute line and the time-saved block. The metric IDs come from
	// the locked label table; values come from the resolver-backed
	// queries so this verb mirrors `abc accounting` and `abc emissions`
	// for the same window.
	if r, ok := ResultByID(results, "spend_zar"); ok && r.Computable {
		if v, ok := r.Value.(float64); ok {
			fmt.Fprintf(w, "%-37s %s %s\n",
				labelOrTech(opts, "Spend this period:", "spend_zar"),
				currencySym, formatThousands(v))
		}
	}
	if r, ok := ResultByID(results, "emissions_kgco2e"); ok && r.Computable {
		if v, ok := r.Value.(float64); ok {
			fmt.Fprintf(w, "%-37s %.1f kg CO₂e\n",
				labelOrTech(opts, "Emissions this period:", "emissions_kgco2e"), v)
		}
	}
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

	// Currency translation. Postdoc rate flows through the same Layer
	// 0/1 resolver `abc accounting` uses — drift between the report and
	// accounting verbs is a regression (see the integration test in
	// integration_test.go).
	hours := 0.0
	if hoursSaved.Computable {
		if v, ok := hoursSaved.Value.(float64); ok {
			hours = v
		}
	}
	postdocRate := card.Cost.PostdocPerHour.Value
	amount := hours * postdocRate
	fmt.Fprintf(w, "%-23s %.1f hours\n", labelOrTech(opts, "Research time saved:", "hours_saved"), hours)
	fmt.Fprintf(w, "%-23s %s %s\n",
		labelOrTech(opts, "Hourly compensation:", "postdoc_per_hour"),
		currencySym, formatRate(postdocRate))
	fmt.Fprintf(w, "%-23s %s %s\n",
		labelOrTech(opts, "Amount:", "amount_zar"),
		currencySym, formatThousands(amount))
	fmt.Fprintln(w)

	// Unified provenance footer: every rate-card AND grid-intensity
	// value used, with layer + citation. Same shape as
	// `abc accounting --rate-source=full`. One block, no duplication.
	writeProvenanceFooter(w, card)
	return nil
}

// resolvedTextCard returns opts.RateCard if populated, otherwise the
// Layer-0 ZA defaults. Lets unit tests skip the wiring boilerplate.
func resolvedTextCard(opts TextOptions) acct.RateCard {
	if opts.RateCard.Currency.Value == "" {
		return acct.ZADefaults()
	}
	return opts.RateCard
}

// currencySymbol returns the ASCII / single-glyph symbol for an ISO
// currency code. We only special-case ZAR (the report verb's seed-tier
// home currency); everything else falls through to the alpha code, with
// a leading space added at the call site so e.g. "USD 1,420" renders
// readably.
func currencySymbol(code string) string {
	if code == "ZAR" {
		return "R"
	}
	return code
}

// formatRate renders a rate value compactly: integer if the value is a
// whole number, two-decimal otherwise. Matches the §"Acceptance" sample
// for the postdoc rate (R 350) while still rendering sub-rand rates
// cleanly (R 0.50/CPU·hr).
func formatRate(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// rateRow is one line in the unified provenance footer. Mirrors the
// shape `internal/accounting.relevantRates` returns so the two verbs
// produce structurally identical footers.
type rateRow struct {
	Key      string
	Value    string
	Source   string
	Citation string
	Updated  time.Time
}

// writeProvenanceFooter emits one footer block enumerating every rate
// card + grid intensity value used. Spec abc-report.md §D' (BINDING):
// "Provenance footer enumerates every rate-card and grid-intensity
// value used, with layer + citation, in the same shape as
// `abc accounting`'s footer. One unified footer; no duplication."
func writeProvenanceFooter(w io.Writer, card acct.RateCard) {
	rows := []rateRow{
		{Key: "currency", Value: card.Currency.Value,
			Source: string(card.Currency.Source), Citation: card.Currency.Citation,
			Updated: card.Currency.UpdatedAt},
		{Key: "cost.cpu_hour", Value: formatRate(card.Cost.CpuHour.Value),
			Source: string(card.Cost.CpuHour.Source), Citation: card.Cost.CpuHour.Citation,
			Updated: card.Cost.CpuHour.UpdatedAt},
		{Key: "cost.gpu_hour", Value: formatRate(card.Cost.GpuHour.Value),
			Source: string(card.Cost.GpuHour.Source), Citation: card.Cost.GpuHour.Citation,
			Updated: card.Cost.GpuHour.UpdatedAt},
		{Key: "cost.memory_gb_hour", Value: formatRate(card.Cost.MemoryGbHour.Value),
			Source: string(card.Cost.MemoryGbHour.Source), Citation: card.Cost.MemoryGbHour.Citation,
			Updated: card.Cost.MemoryGbHour.UpdatedAt},
		{Key: "cost.storage_scratch_gb_hour", Value: strconv.FormatFloat(card.Cost.StorageScratchGbHour.Value, 'g', -1, 64),
			Source: string(card.Cost.StorageScratchGbHour.Source), Citation: card.Cost.StorageScratchGbHour.Citation,
			Updated: card.Cost.StorageScratchGbHour.UpdatedAt},
		{Key: "cost.postdoc_per_hour", Value: formatRate(card.Cost.PostdocPerHour.Value),
			Source: string(card.Cost.PostdocPerHour.Source), Citation: card.Cost.PostdocPerHour.Citation,
			Updated: card.Cost.PostdocPerHour.UpdatedAt},
		{Key: "emissions.grid_factor_gco2_per_kwh", Value: formatRate(card.Emissions.GridFactorGco2PerKwh.Value),
			Source: string(card.Emissions.GridFactorGco2PerKwh.Source), Citation: card.Emissions.GridFactorGco2PerKwh.Citation,
			Updated: card.Emissions.GridFactorGco2PerKwh.UpdatedAt},
		{Key: "emissions.cpu_w", Value: formatRate(card.Emissions.CpuW.Value),
			Source: string(card.Emissions.CpuW.Source), Citation: card.Emissions.CpuW.Citation,
			Updated: card.Emissions.CpuW.UpdatedAt},
		{Key: "emissions.gpu_w", Value: formatRate(card.Emissions.GpuW.Value),
			Source: string(card.Emissions.GpuW.Source), Citation: card.Emissions.GpuW.Citation,
			Updated: card.Emissions.GpuW.UpdatedAt},
		{Key: "emissions.memory_gb_w", Value: strconv.FormatFloat(card.Emissions.MemoryGbW.Value, 'g', -1, 64),
			Source: string(card.Emissions.MemoryGbW.Source), Citation: card.Emissions.MemoryGbW.Citation,
			Updated: card.Emissions.MemoryGbW.UpdatedAt},
		{Key: "emissions.pue", Value: formatRate(card.Emissions.Pue.Value),
			Source: string(card.Emissions.Pue.Source), Citation: card.Emissions.Pue.Citation,
			Updated: card.Emissions.Pue.UpdatedAt},
		{Key: "emissions.storage_scratch_w_per_tb", Value: formatRate(card.Emissions.StorageScratchWPerTb.Value),
			Source: string(card.Emissions.StorageScratchWPerTb.Source), Citation: card.Emissions.StorageScratchWPerTb.Citation,
			Updated: card.Emissions.StorageScratchWPerTb.UpdatedAt},
	}

	fmt.Fprintln(w, "Rate card (effective):")
	maxKey := 0
	for _, r := range rows {
		if len(r.Key) > maxKey {
			maxKey = len(r.Key)
		}
	}
	for _, r := range rows {
		src := r.Source
		if src == "" {
			src = string(acct.SourceBuiltIn)
		}
		fmt.Fprintf(w, "  %-*s  %-8s  %-10s  (%s)\n", maxKey, r.Key, r.Value, src, r.Citation)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "These rates are showback estimates; not invoice-grade. To override:")
	fmt.Fprintln(w, "  abc config accounting set cost.postdoc_per_hour=400")
	fmt.Fprintln(w, "  abc config emissions set pue=1.27 grid_factor_gco2_per_kwh=950")
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
