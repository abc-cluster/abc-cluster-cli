package accounting

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// GroupBy enumerates the supported aggregation axes for accounting/emissions
// reports.
type GroupBy string

const (
	GroupByNamespace     GroupBy = "namespace"
	GroupByProject       GroupBy = "project"
	GroupByInvestigation GroupBy = "investigation"
	GroupByUser          GroupBy = "user"
	GroupByPipeline      GroupBy = "pipeline"
)

// ReportMode picks between cost (accounting) and emissions reports.
type ReportMode string

const (
	ModeAccounting ReportMode = "accounting"
	ModeEmissions  ReportMode = "emissions"
)

// EmissionsUnit picks the display unit for the emissions report.
type EmissionsUnit string

const (
	UnitKg EmissionsUnit = "kg"
	UnitT  EmissionsUnit = "t"
	UnitG  EmissionsUnit = "g"
)

// RateSourceVerbosity picks the verbosity of the rate-card footer.
type RateSourceVerbosity string

const (
	RateSourceFull  RateSourceVerbosity = "full"
	RateSourceBrief RateSourceVerbosity = "brief"
	RateSourceNone  RateSourceVerbosity = "none"
)

// OutputFormat enumerates render targets.
type OutputFormat string

const (
	OutputTable OutputFormat = "table"
	OutputCSV   OutputFormat = "csv"
	OutputJSON  OutputFormat = "json"
)

// ReportOptions configure a single report invocation.
type ReportOptions struct {
	Mode              ReportMode
	By                GroupBy
	Since             time.Time
	Until             time.Time
	ContextName       string
	IncludeIncomplete bool
	Output            OutputFormat
	RateSource        RateSourceVerbosity
	EmissionsUnit     EmissionsUnit // only for ModeEmissions
}

// Row is one aggregated line in the report.
type Row struct {
	Group string  `json:"group"`
	// Total is in the report's currency / emissions unit.
	Total float64 `json:"total"`
	// Inputs (for transparency).
	CpuHours         float64 `json:"cpu_hours"`
	MemoryGbHours    float64 `json:"memory_gb_hours"`
	GpuHours         float64 `json:"gpu_hours"`
	ScratchGbHours   float64 `json:"scratch_gb_hours"`
	WalltimeHours    float64 `json:"walltime_hours"`
	Runs             int64   `json:"runs"`
}

// Report is the full computed result: header + rows + effective rate card.
type Report struct {
	Mode    ReportMode `json:"mode"`
	By      GroupBy    `json:"by"`
	Since   time.Time  `json:"since"`
	Until   time.Time  `json:"until"`
	Unit    string     `json:"unit"`
	Rows    []Row      `json:"rows"`
	RateCard RateCard  `json:"rate_card"`
}

// Aggregate runs the SQL aggregation and returns rows in descending total
// order. Caller has already resolved the RateCard; Aggregate reads from the
// `runs` table at db.
func Aggregate(ctx context.Context, db *sql.DB, opts ReportOptions, card RateCard) (Report, error) {
	rep := Report{
		Mode:     opts.Mode,
		By:       opts.By,
		Since:    opts.Since,
		Until:    opts.Until,
		RateCard: card,
	}
	if opts.Mode == ModeEmissions {
		rep.Unit = string(opts.EmissionsUnit)
		if rep.Unit == "" {
			rep.Unit = string(UnitKg)
		}
	} else {
		rep.Unit = card.Currency.Value
	}

	groupCol, err := groupColumn(opts.By)
	if err != nil {
		return rep, err
	}
	now := time.Now().Unix()
	args := []any{opts.ContextName, opts.Since.Unix(), opts.Until.Unix()}

	// Two SELECTs unioned: completed runs use stored cpu_hours/memory_gb_hours;
	// incomplete (running) runs synthesise them from (now - submitted_at) and
	// COALESCE(walltime_seconds_so_far) using the directly-tracked CPU
	// hours = (now - submitted_at) seconds / 3600 only when those fields are
	// populated. For simplicity and because cpu_hours / memory_gb_hours are
	// only set on completion, the incomplete branch surfaces zeros for cost
	// per spec's "cost-so-far"; full implementation needs Voron metrics.
	q := `
		SELECT ` + groupCol + ` AS group_col,
		       COALESCE(SUM(cpu_hours), 0)            AS cpu_hours,
		       COALESCE(SUM(memory_gb_hours), 0)      AS memory_gb_hours,
		       COALESCE(SUM(CAST(gpu_count AS REAL) * (CAST(walltime_seconds AS REAL) / 3600.0)), 0) AS gpu_hours,
		       COALESCE(SUM(CAST(scratch_gb AS REAL) * (CAST(walltime_seconds AS REAL) / 3600.0)), 0) AS scratch_gb_hours,
		       COALESCE(SUM(CAST(walltime_seconds AS REAL) / 3600.0), 0) AS walltime_hours,
		       COUNT(*) AS runs
		FROM runs
		WHERE context_name = ?
		  AND submitted_at >= ?
		  AND submitted_at <= ?`

	if opts.IncludeIncomplete {
		// Include both completed and running rows.
		q += ` AND (completed_at IS NOT NULL OR status = 'running')`
	} else {
		q += ` AND completed_at IS NOT NULL`
	}
	q += ` GROUP BY group_col HAVING group_col IS NOT NULL ORDER BY group_col`

	_ = now // reserved for future cost-so-far implementation

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return rep, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r Row
		var gc sql.NullString
		if err := rows.Scan(&gc, &r.CpuHours, &r.MemoryGbHours, &r.GpuHours, &r.ScratchGbHours, &r.WalltimeHours, &r.Runs); err != nil {
			return rep, err
		}
		if !gc.Valid || gc.String == "" {
			continue
		}
		r.Group = gc.String
		switch opts.Mode {
		case ModeAccounting:
			// Compute cost includes scratch GB·hour. Persistent storage is
			// project-scoped (not per-run) and arrives via a separate
			// `abc accounting storage` verb in Phase 1.D.
			r.Total = r.CpuHours*card.Cost.CpuHour.Value +
				r.GpuHours*card.Cost.GpuHour.Value +
				r.MemoryGbHours*card.Cost.MemoryGbHour.Value +
				r.ScratchGbHours*card.Cost.StorageScratchGbHour.Value
		case ModeEmissions:
			// Scratch contribution: GB·hour × (W/TB ÷ 1000 GB/TB) = W·hour
			//                      ÷ 1000 → kWh; PUE applied to the sum.
			scratchEnergyKwh := r.ScratchGbHours * card.Emissions.StorageScratchWPerTb.Value /
				1000.0 / 1000.0
			energyKwh := ((r.CpuHours*card.Emissions.CpuW.Value +
				r.GpuHours*card.Emissions.GpuW.Value +
				r.MemoryGbHours*card.Emissions.MemoryGbW.Value) / 1000.0) *
				card.Emissions.Pue.Value
			energyKwh += scratchEnergyKwh * card.Emissions.Pue.Value
			co2Kg := energyKwh * card.Emissions.GridFactorGco2PerKwh.Value / 1000.0
			r.Total = convertCO2Kg(co2Kg, opts.EmissionsUnit)
		}
		rep.Rows = append(rep.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}
	return rep, nil
}

func convertCO2Kg(kg float64, unit EmissionsUnit) float64 {
	switch unit {
	case UnitT:
		return kg / 1000.0
	case UnitG:
		return kg * 1000.0
	default:
		return kg
	}
}

func groupColumn(by GroupBy) (string, error) {
	switch by {
	case GroupByNamespace:
		// COALESCE so runs with no explicit namespace (seedling tier, Nomad -dev)
		// aggregate under "default" rather than being filtered out by HAVING IS NOT NULL.
		return "COALESCE(namespace, 'default')", nil
	case GroupByProject:
		return "project_id", nil
	case GroupByInvestigation:
		return "investigation_id", nil
	case GroupByPipeline:
		return "workload_ref", nil
	case GroupByUser:
		return "context_name", nil // closest local proxy: per-context user; admin-gated upstream
	}
	return "", fmt.Errorf("unsupported --by=%q (allowed: namespace, project, investigation, user, pipeline)", by)
}

// Render writes the report to w in the requested output format. For table
// mode, RateSource controls the footer verbosity. JSON mode always
// includes the full rate card; CSV mode never includes the footer on
// stdout (the JSON form is the rate-card-bearing alternative).
func Render(w io.Writer, rep Report, opts ReportOptions) error {
	switch opts.Output {
	case OutputJSON:
		return renderJSON(w, rep)
	case OutputCSV:
		return renderCSV(w, rep)
	default:
		return renderTable(w, rep, opts)
	}
}

func renderTable(w io.Writer, rep Report, opts ReportOptions) error {
	header := tableHeader(rep)
	groupCol := titleCase(string(rep.By))
	fmt.Fprintf(w, "%-28s %s\n", groupCol, header)
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 28+len(header)))
	if len(rep.Rows) == 0 {
		fmt.Fprintln(w, "(no runs in this window)")
	}
	var grandTotal float64
	for _, r := range rep.Rows {
		fmt.Fprintf(w, "%-28s %.2f\n", truncate(r.Group, 28), r.Total)
		grandTotal += r.Total
	}
	if len(rep.Rows) > 1 {
		sep := strings.Repeat("-", 28+len(header))
		fmt.Fprintf(w, "%s\n", sep)
		fmt.Fprintf(w, "%-28s %.2f\n", "Total", grandTotal)
	} else if len(rep.Rows) == 1 {
		// Single namespace — still show Total so the line is always present.
		fmt.Fprintf(w, "%-28s %.2f\n", "Total (estimated)", grandTotal)
	}
	verbosity := opts.RateSource
	if verbosity == "" {
		verbosity = RateSourceFull
	}
	if verbosity == RateSourceNone {
		return nil
	}
	fmt.Fprintln(w)
	if verbosity == RateSourceBrief {
		writeBriefRateCard(w, rep)
		return nil
	}
	writeFullRateCard(w, rep)
	return nil
}

func tableHeader(rep Report) string {
	if rep.Mode == ModeEmissions {
		unit := rep.Unit
		if unit == "" {
			unit = "kg"
		}
		return "Emissions (" + unit + " CO2e)"
	}
	curr := rep.RateCard.Currency.Value
	if curr == "" {
		curr = "ZAR"
	}
	return "Cost (" + curr + ")"
}

// writeFullRateCard prints every rate used in the calculation with its
// source, last-update timestamp, and citation.
func writeFullRateCard(w io.Writer, rep Report) {
	fmt.Fprintln(w, "Rate card (effective):")
	rates := relevantRates(rep)
	maxKey := 0
	for _, r := range rates {
		if len(r.Key) > maxKey {
			maxKey = len(r.Key)
		}
	}
	for _, r := range rates {
		fmt.Fprintf(w, "  %-*s  %-8s  %-10s  (%s)\n",
			maxKey, r.Key, formatValue(r.Value, r.IsString, r.StringVal),
			r.Source, formatProvenance(r))
	}
	fmt.Fprintln(w)
	if rep.Mode == ModeAccounting {
		fmt.Fprintln(w, "These figures are rough estimates based on built-in default rates.")
		fmt.Fprintln(w, "In future releases, values will reflect dynamic resource measurements")
		fmt.Fprintln(w, "from the cluster (CPU utilisation, memory, walltime via Nomad telemetry).")
	} else {
		fmt.Fprintln(w, "These figures are rough estimates based on built-in default factors.")
		fmt.Fprintln(w, "In future releases, emissions will be computed from live grid intensity")
		fmt.Fprintln(w, "data and measured resource utilisation rather than static coefficients.")
	}
}

func writeBriefRateCard(w io.Writer, rep Report) {
	rates := relevantRates(rep)
	userSet, builtIn, flagged := 0, 0, 0
	for _, r := range rates {
		switch r.Source {
		case string(SourceConfig):
			userSet++
		case string(SourceBuiltIn):
			builtIn++
		case string(SourceFlag):
			flagged++
		}
	}
	parts := []string{}
	if userSet > 0 {
		parts = append(parts, fmt.Sprintf("%d user-set", userSet))
	}
	if flagged > 0 {
		parts = append(parts, fmt.Sprintf("%d flag-overridden", flagged))
	}
	if builtIn > 0 {
		parts = append(parts, fmt.Sprintf("%d built-in", builtIn))
	}
	fmt.Fprintf(w, "Rates: %s (SA defaults %s)\n", strings.Join(parts, ", "), defaultsTag())
}

func defaultsTag() string {
	// Mirrors the citation text used in the full footer.
	return "abc-cluster-cli " + clivPlaceholder()
}

// rateRow is an internal flattened view of a single line in the rate card
// footer.
type rateRow struct {
	Key       string
	Value     float64
	IsString  bool
	StringVal string
	Source    string
	Citation  string
	UpdatedAt time.Time
}

func relevantRates(rep Report) []rateRow {
	rc := rep.RateCard
	var out []rateRow
	if rep.Mode == ModeAccounting {
		out = append(out,
			fromRateValue("cost.cpu_hour", rc.Cost.CpuHour),
			fromRateValue("cost.gpu_hour", rc.Cost.GpuHour),
			fromRateValue("cost.memory_gb_hour", rc.Cost.MemoryGbHour),
			fromRateValue("cost.storage_scratch_gb_hour", rc.Cost.StorageScratchGbHour),
			fromRateString("currency", rc.Currency),
		)
	} else {
		out = append(out,
			fromRateValue("emissions.grid_factor", rc.Emissions.GridFactorGco2PerKwh),
			fromRateValue("emissions.cpu_w", rc.Emissions.CpuW),
			fromRateValue("emissions.gpu_w", rc.Emissions.GpuW),
			fromRateValue("emissions.memory_gb_w", rc.Emissions.MemoryGbW),
			fromRateValue("emissions.pue", rc.Emissions.Pue),
			fromRateValue("emissions.storage_scratch_w_per_tb", rc.Emissions.StorageScratchWPerTb),
		)
	}
	return out
}

func fromRateValue(key string, rv RateValue) rateRow {
	return rateRow{
		Key:       key,
		Value:     rv.Value,
		Source:    string(rv.Source),
		Citation:  rv.Citation,
		UpdatedAt: rv.UpdatedAt,
	}
}

func fromRateString(key string, rs RateString) rateRow {
	return rateRow{
		Key:       key,
		IsString:  true,
		StringVal: rs.Value,
		Source:    string(rs.Source),
		Citation:  rs.Citation,
		UpdatedAt: rs.UpdatedAt,
	}
}

func formatValue(v float64, isString bool, s string) string {
	if isString {
		return s
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatProvenance(r rateRow) string {
	if r.Citation != "" {
		return r.Citation
	}
	if r.Source == string(SourceLocal) && !r.UpdatedAt.IsZero() {
		return "~/.abc/config.yaml mtime " + r.UpdatedAt.Format("2006-01-02 15:04") + "  [advisory]"
	}
	if r.Source == string(SourceFlag) {
		return "this invocation  [advisory]"
	}
	return ""
}

// clivPlaceholder returns the running CLI version string. Indirected to
// avoid a hard dep loop with cmd/root.go init order in tests.
func clivPlaceholder() string {
	return clivResolver()
}

// clivResolver is overridable in tests.
var clivResolver = func() string { return "" }

func init() {
	// Default to the state package's CLIVersion (set by cmd/root.go init).
	// We can't import internal/state here without churn — the value is
	// already injected at runtime via the citation strings in defaults_za.go.
	// brief footer is best-effort.
	clivResolver = func() string { return "(see full footer)" }
}

func renderJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func renderCSV(w io.Writer, rep Report) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	header := []string{string(rep.By), "total", "unit", "cpu_hours", "memory_gb_hours", "gpu_hours", "scratch_gb_hours", "walltime_hours", "runs"}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range rep.Rows {
		row := []string{
			r.Group,
			strconv.FormatFloat(r.Total, 'f', -1, 64),
			rep.Unit,
			strconv.FormatFloat(r.CpuHours, 'f', -1, 64),
			strconv.FormatFloat(r.MemoryGbHours, 'f', -1, 64),
			strconv.FormatFloat(r.GpuHours, 'f', -1, 64),
			strconv.FormatFloat(r.ScratchGbHours, 'f', -1, 64),
			strconv.FormatFloat(r.WalltimeHours, 'f', -1, 64),
			strconv.FormatInt(r.Runs, 10),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
