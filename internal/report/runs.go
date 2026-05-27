// runs.go — backing store for `abc report runs`.
//
// Returns a windowed, paginated, jobs-first list of run rows from
// ~/.abc/local.db with cost + emissions DERIVED at read time from the
// row's stored resources × the resolved rate card.
//
// Scale: designed to handle thousands of rows per user. The query is
// limited and ordered through the composite index added in migration
// 0012 (idx_runs_context_verb_submitted). The renderer never holds
// more than `limit` rows in memory.
//
// Pipelines vs jobs (brainstorm: brainstorms/abc-report-use-cases/
// 2026-05-27-…):
//   • Job rows carry honest cost — the watcher reads the single-alloc
//     resources at completion. CostPending is false; numbers are
//     populated.
//   • Pipeline rows carry only the head-orchestrator resources today
//     (worker allocs aren't summed; the cache aggregator that will
//     do that is on the Khan roadmap). For these rows the renderer
//     sets CostPending=true and emits "—" in the cost / CO₂e cells
//     so we never present an order-of-magnitude undercount as truth.

package report

import (
	"context"
	"database/sql"
	"strings"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// RunRow is one row in `abc report runs` output. The default text
// renderer surfaces only the columnar fields (time, verb, workload,
// cpu, cost, co2e, status); the forensic fields below are surfaced
// only when --full is passed (per-row vertical block layout) or in
// the JSON output (always present).
type RunRow struct {
	// Default columnar fields ─────────────────────────────────────
	RunID           string
	Verb            string // 'job' | 'pipeline'
	WorkloadRef     string // e.g. 'nf-core/sarek' or 'samtools sort …'
	WorkloadVersion string
	Status          string
	SubmittedAt     time.Time
	CompletedAt     *time.Time
	CPUHours        float64
	MemGBHours      float64
	WalltimeSec     int64
	// CostZAR / EmissionsKgCO2e are derived. CostPending=true when
	// the underlying resource numbers are known to be incomplete
	// (pipeline head-only today) — renderer should print "—" instead.
	CostZAR         float64
	EmissionsKgCO2e float64
	CostPending     bool

	// Forensic fields (surfaced via --full) ───────────────────────
	// Filled by the watcher / autoattach; "" / 0 for older rows or
	// runs that took a code path that didn't write the field.
	ExitReason      string  // CompleteRun fills this (e.g. "Exit Code: 1")
	ExitCode        int64   // raw alloc task exit code (migration 0013)
	NomadJobID      string  // for the cross-tool jump to `abc job show`
	Namespace       string  // Nomad namespace the run was submitted into
	ProjectID       string  // auto-attached project (NULL → "")
	InvestigationID string  // auto-attached investigation (NULL → "")
	FreezeID        string  // pinned dependency set (NULL → "")
	WorkdirRoot     string  // pipeline resume-key prefix (NULL → "")
	ParamsJSON      string  // raw params blob captured at submit time
}

// RunsQuery is the windowed/paged query for `abc report runs`.
type RunsQuery struct {
	ContextName string
	Since       time.Time
	Until       time.Time
	Verb        string // "" (all), "job", "pipeline"
	Limit       int    // 0 means default (20)
	Offset      int
}

// RunsResult is the resolved page + the total count (so the renderer
// can show "N of M").
type RunsResult struct {
	Rows  []RunRow
	Total int // total rows matching the filter (before LIMIT/OFFSET)
}

// QueryRuns reads the runs table, derives cost+emissions per row, and
// returns at most `Limit` rows. Jobs lead pipelines (verb ASC), then
// most-recent first within each verb.
func QueryRuns(ctx context.Context, db *sql.DB, q RunsQuery, card acct.RateCard) (RunsResult, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 1000 {
		q.Limit = 1000
	}

	// Count first so the footer can show "N of M". The count uses the
	// same WHERE clause but no ORDER/LIMIT — cheap with the composite
	// index even at 10k rows.
	whereSQL, whereArgs := runsWhere(q)
	var total int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs "+whereSQL, whereArgs...).Scan(&total); err != nil {
		return RunsResult{}, err
	}

	args := append([]any{}, whereArgs...)
	args = append(args, q.Limit, q.Offset)
	// Forensic columns (exit_code, nomad_job_id, namespace, project_id,
	// investigation_id, freeze_id, workdir_root, params_json,
	// exit_reason) are fetched on every QueryRuns call — the columnar
	// renderer just ignores them when --full isn't set. This keeps the
	// JSON output complete (every consumer gets the same shape) and
	// dodges a duplicate SELECT path for the --full case at the cost
	// of ~150 extra bytes per row, which is irrelevant at limit=1000.
	rowsSQL := `
		SELECT run_id, verb, workload_ref, COALESCE(workload_version,''),
		       COALESCE(status,''), submitted_at, completed_at,
		       COALESCE(cpu_hours, 0), COALESCE(memory_gb_hours, 0),
		       COALESCE(walltime_seconds, 0),
		       COALESCE(exit_reason,''), COALESCE(exit_code, 0),
		       COALESCE(nomad_job_id,''), COALESCE(namespace,''),
		       COALESCE(project_id,''), COALESCE(investigation_id,''),
		       COALESCE(freeze_id,''), COALESCE(workdir_root,''),
		       COALESCE(params_json,'')
		  FROM runs ` + whereSQL + `
		 ORDER BY verb ASC, submitted_at DESC
		 LIMIT ? OFFSET ?`
	rows, err := db.QueryContext(ctx, rowsSQL, args...)
	if err != nil {
		return RunsResult{}, err
	}
	defer rows.Close()

	out := make([]RunRow, 0, q.Limit)
	for rows.Next() {
		var r RunRow
		var submittedAt int64
		var completedAt sql.NullInt64
		if err := rows.Scan(&r.RunID, &r.Verb, &r.WorkloadRef, &r.WorkloadVersion,
			&r.Status, &submittedAt, &completedAt,
			&r.CPUHours, &r.MemGBHours, &r.WalltimeSec,
			&r.ExitReason, &r.ExitCode,
			&r.NomadJobID, &r.Namespace,
			&r.ProjectID, &r.InvestigationID,
			&r.FreezeID, &r.WorkdirRoot,
			&r.ParamsJSON); err != nil {
			return RunsResult{}, err
		}
		r.SubmittedAt = time.Unix(submittedAt, 0).UTC()
		if completedAt.Valid {
			t := time.Unix(completedAt.Int64, 0).UTC()
			r.CompletedAt = &t
		}

		// Derive cost + emissions. For pipelines we ONLY have head-
		// orchestrator resources today; flagging CostPending tells
		// the renderer to render "—" instead of a misleading number.
		r.CostZAR, r.EmissionsKgCO2e = deriveCostEmissions(r, card)
		if r.Verb == "pipeline" {
			r.CostPending = true
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return RunsResult{}, err
	}
	return RunsResult{Rows: out, Total: total}, nil
}

// runsWhere builds the WHERE clause + args for the query. Kept
// separate so COUNT(*) and the rows query share an identical filter.
func runsWhere(q RunsQuery) (string, []any) {
	clauses := []string{"context_name = ?"}
	args := []any{q.ContextName}

	if !q.Since.IsZero() {
		clauses = append(clauses, "submitted_at >= ?")
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		clauses = append(clauses, "submitted_at <= ?")
		args = append(args, q.Until.Unix())
	}
	switch q.Verb {
	case "job", "pipeline":
		clauses = append(clauses, "verb = ?")
		args = append(args, q.Verb)
	case "":
		// no filter
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// deriveCostEmissions reproduces the same arithmetic the headline
// `abc report` uses (CPU·hr × rate + MEM·GB·hr × rate, scaled by PUE
// for emissions). Same rate card resolved through the same
// Layer-0/1/2 path; one source of truth for the multiplication.
func deriveCostEmissions(r RunRow, card acct.RateCard) (costZAR, emissionsKg float64) {
	cpuRate := card.Cost.CpuHour.Value
	memRate := card.Cost.MemoryGbHour.Value
	costZAR = r.CPUHours*cpuRate + r.MemGBHours*memRate

	cpuW := card.Emissions.CpuW.Value
	memW := card.Emissions.MemoryGbW.Value
	pue := card.Emissions.Pue.Value
	if pue == 0 {
		pue = 1.0
	}
	gridG := card.Emissions.GridFactorGco2PerKwh.Value
	// energy_kWh = (cpu_W × cpu_hours + mem_W × mem_gb_hours) × PUE / 1000
	energyKWh := (cpuW*r.CPUHours + memW*r.MemGBHours) * pue / 1000.0
	emissionsKg = energyKWh * gridG / 1000.0 // gCO2 → kgCO2
	return costZAR, emissionsKg
}
