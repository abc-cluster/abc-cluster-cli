package report

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// Window is the time range a metric is computed over. Always inclusive
// at both ends. Default is YTD when --since/--until are unset.
type Window struct {
	Since time.Time
	Until time.Time
}

// QueryOptions holds inputs every metric query needs.
type QueryOptions struct {
	Window      Window
	ContextName string // limit to a single context; "" = all contexts
	// GroupColumn / GroupKey scope the metric query to a single group
	// (investigation_id / project_id / workload_ref / context_name). Set
	// by the JSON --by=<axis> path; empty in the personal-summary path.
	GroupColumn string
	GroupKey    string
}

// MetricResult is the per-metric output. Computable=false with a Reason
// renders as "n/a (reason)" in the text renderer rather than zeroing
// silently. Spec abc-report.md §C.
type MetricResult struct {
	ID         string
	Value      any
	Computable bool
	Reason     string
}

// MissingColumnReason is the message to show when a metric needs a
// migration that hasn't been applied yet. The text matches the spec's
// §F acceptance criterion verbatim so capability tooling can grep it.
func MissingColumnReason(migration string) string {
	return fmt.Sprintf("requires migration %s data; run `abc localdb migrate`", migration)
}

// Compute returns the result for every metric in Metrics, in the same
// order. Each metric is independent; a single missing column does not
// fail the whole report.
func Compute(ctx context.Context, db *sql.DB, opts QueryOptions) []MetricResult {
	out := make([]MetricResult, 0, len(Metrics))
	for _, m := range Metrics {
		out = append(out, computeOne(ctx, db, m.ID, opts))
	}
	return out
}

// ResultByID is a small helper for tests and the text renderer.
func ResultByID(rs []MetricResult, id string) (MetricResult, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return MetricResult{}, false
}

func computeOne(ctx context.Context, db *sql.DB, id string, opts QueryOptions) MetricResult {
	switch id {
	case "mttfs":
		return queryMTTFS(ctx, db, opts)
	case "failure_to_result_ratio":
		return queryFailureToResultRatio(ctx, db, opts)
	case "retry_depth":
		return queryRetryDepth(ctx, db, opts)
	case "mttr_failure":
		return queryMTTRFailure(ctx, db, opts)
	case "stabilisation_runs":
		return queryStabilisationRuns(ctx, db, opts)
	case "queue_wait_fraction":
		return queryQueueWaitFraction(ctx, db, opts)
	case "active_engagement_hours":
		return queryActiveEngagementHours(ctx, db, opts)
	case "spectator_hours":
		return querySpectatorHours(ctx, db, opts)
	case "async_job_fraction":
		return queryAsyncJobFraction(ctx, db, opts)
	case "cognitive_overhead_score":
		return queryCognitiveOverheadScore(ctx, db, opts)
	case "workflows_unattended":
		return queryWorkflowsUnattended(ctx, db, opts)
	case "submission_source":
		return querySubmissionSource(ctx, db, opts)
	case "resource_fit":
		return queryResourceFit(ctx, db, opts)
	case "cost_per_investigation":
		return queryCostPerInvestigation(ctx, db, opts)
	case "hours_saved":
		// hours_saved depends on several other queries; compute them
		// inline here rather than re-running.
		return queryHoursSaved(ctx, db, opts)
	}
	return MetricResult{ID: id, Computable: false, Reason: "unknown metric"}
}

// ---- helpers ----

// runWindowClause returns the SQL fragment + args to scope a query to
// runs in the report window for the given context.
func runWindowClause(opts QueryOptions) (string, []any) {
	clauses := []string{"submitted_at >= ?", "submitted_at <= ?"}
	args := []any{opts.Window.Since.Unix(), opts.Window.Until.Unix()}
	if opts.ContextName != "" {
		clauses = append(clauses, "context_name = ?")
		args = append(args, opts.ContextName)
	}
	if opts.GroupColumn != "" {
		// GroupColumn is internal-only (set by cmd/report from a fixed
		// allowlist); GroupKey may be a sentinel "(none)" matching the
		// COALESCE in the group-key discovery query.
		clauses = append(clauses, fmt.Sprintf("COALESCE(%s, '(none)') = ?", opts.GroupColumn))
		args = append(args, opts.GroupKey)
	}
	return strings.Join(clauses, " AND "), args
}

// auditWindowClause returns SQL + args for the cli_audit table.
func auditWindowClause(opts QueryOptions) (string, []any) {
	clauses := []string{"invoked_at >= ?", "invoked_at <= ?"}
	args := []any{opts.Window.Since.Unix(), opts.Window.Until.Unix()}
	if opts.ContextName != "" {
		clauses = append(clauses, "context_name = ?")
		args = append(args, opts.ContextName)
	}
	return strings.Join(clauses, " AND "), args
}

// ---- per-metric queries ----

// queryMTTFS: hours from earliest submitted_at to first successful
// completed_at in the window. Empty window → 0 (computable, value=0).
func queryMTTFS(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT MIN(submitted_at), MIN(CASE WHEN status = 'completed' THEN completed_at END)
		FROM runs WHERE %s`, where)
	var earliest sql.NullInt64
	var firstSuccess sql.NullInt64
	if err := db.QueryRowContext(ctx, q, args...).Scan(&earliest, &firstSuccess); err != nil {
		return MetricResult{ID: "mttfs", Computable: false, Reason: err.Error()}
	}
	if !earliest.Valid || !firstSuccess.Valid {
		return MetricResult{ID: "mttfs", Value: 0.0, Computable: true}
	}
	hours := float64(firstSuccess.Int64-earliest.Int64) / 3600.0
	if hours < 0 {
		hours = 0
	}
	return MetricResult{ID: "mttfs", Value: hours, Computable: true}
}

// queryFailureToResultRatio: failed_runs / completed_runs in the window.
// Returns 0 when no completed runs (computable, no failures = 0 ratio).
func queryFailureToResultRatio(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END)
		FROM runs WHERE %s`, where)
	var failed, completed sql.NullInt64
	if err := db.QueryRowContext(ctx, q, args...).Scan(&failed, &completed); err != nil {
		return MetricResult{ID: "failure_to_result_ratio", Computable: false, Reason: err.Error()}
	}
	if !completed.Valid || completed.Int64 == 0 {
		return MetricResult{ID: "failure_to_result_ratio", Value: 0.0, Computable: true}
	}
	ratio := float64(failed.Int64) / float64(completed.Int64)
	return MetricResult{ID: "failure_to_result_ratio", Value: ratio, Computable: true}
}

// queryRetryDepth: average count of attempts (workload_ref grouping)
// before a successful completion within the window. A workload that
// succeeded first try contributes 1; one with three retries before
// success contributes 4.
func queryRetryDepth(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT workload_ref, status, submitted_at
		FROM runs WHERE %s
		ORDER BY workload_ref, submitted_at`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return MetricResult{ID: "retry_depth", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	depths := []int{}
	var (
		curRef    string
		curDepth  int
	)
	flush := func() {
		if curRef != "" && curDepth > 0 {
			depths = append(depths, curDepth)
		}
	}
	for rows.Next() {
		var ref, status string
		var ts int64
		if err := rows.Scan(&ref, &status, &ts); err != nil {
			return MetricResult{ID: "retry_depth", Computable: false, Reason: err.Error()}
		}
		if ref != curRef {
			flush()
			curRef = ref
			curDepth = 0
		}
		curDepth++
		if status == "completed" {
			depths = append(depths, curDepth)
			curRef = ""
			curDepth = 0
		}
	}
	flush()
	if len(depths) == 0 {
		return MetricResult{ID: "retry_depth", Value: 0.0, Computable: true}
	}
	sum := 0
	for _, d := range depths {
		sum += d
	}
	avg := float64(sum) / float64(len(depths))
	return MetricResult{ID: "retry_depth", Value: avg, Computable: true}
}

// queryMTTRFailure: hours between a failed run and the next succeeded
// run for the same workload_ref. Averaged across all such pairs in the
// window.
func queryMTTRFailure(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT workload_ref, status, submitted_at, completed_at
		FROM runs WHERE %s
		ORDER BY workload_ref, submitted_at`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return MetricResult{ID: "mttr_failure", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var (
		curRef        string
		lastFailEnd   int64
		haveFail      bool
		recoveryHours []float64
	)
	for rows.Next() {
		var ref, status string
		var subAt int64
		var compAt sql.NullInt64
		if err := rows.Scan(&ref, &status, &subAt, &compAt); err != nil {
			return MetricResult{ID: "mttr_failure", Computable: false, Reason: err.Error()}
		}
		if ref != curRef {
			curRef = ref
			lastFailEnd = 0
			haveFail = false
		}
		if status == "failed" {
			if compAt.Valid {
				lastFailEnd = compAt.Int64
			} else {
				lastFailEnd = subAt
			}
			haveFail = true
			continue
		}
		if status == "completed" && haveFail && compAt.Valid {
			h := float64(compAt.Int64-lastFailEnd) / 3600.0
			if h >= 0 {
				recoveryHours = append(recoveryHours, h)
			}
			haveFail = false
		}
	}
	if len(recoveryHours) == 0 {
		return MetricResult{ID: "mttr_failure", Value: 0.0, Computable: true}
	}
	sum := 0.0
	for _, h := range recoveryHours {
		sum += h
	}
	return MetricResult{ID: "mttr_failure", Value: sum / float64(len(recoveryHours)), Computable: true}
}

// queryStabilisationRuns: count of failed runs preceding the FIRST
// successful run for each workload_ref in the window, summed. Captures
// "how many tries before things settled".
func queryStabilisationRuns(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT workload_ref, status
		FROM runs WHERE %s
		ORDER BY workload_ref, submitted_at`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return MetricResult{ID: "stabilisation_runs", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var (
		curRef     string
		curFails   int
		seenSucc   bool
		totalFails int
	)
	flush := func() {
		if seenSucc {
			totalFails += curFails
		}
	}
	for rows.Next() {
		var ref, status string
		if err := rows.Scan(&ref, &status); err != nil {
			return MetricResult{ID: "stabilisation_runs", Computable: false, Reason: err.Error()}
		}
		if ref != curRef {
			flush()
			curRef = ref
			curFails = 0
			seenSucc = false
		}
		if seenSucc {
			continue
		}
		if status == "failed" {
			curFails++
		}
		if status == "completed" {
			seenSucc = true
		}
	}
	flush()
	return MetricResult{ID: "stabilisation_runs", Value: float64(totalFails), Computable: true}
}

// queryQueueWaitFraction: avg pending_seconds / (pending_seconds +
// walltime_seconds) across runs that have BOTH columns populated.
// Spec abc-report.md §C: "n/a" when no row has pending_seconds.
func queryQueueWaitFraction(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT pending_seconds, walltime_seconds
		FROM runs WHERE %s AND pending_seconds IS NOT NULL`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		// Table-shape error before any row read = column missing.
		if isMissingColumnErr(err) {
			return MetricResult{ID: "queue_wait_fraction", Computable: false,
				Reason: MissingColumnReason("0008_runs_queue_wait")}
		}
		return MetricResult{ID: "queue_wait_fraction", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var (
		total float64
		n     int
	)
	for rows.Next() {
		var pending sql.NullInt64
		var walltime sql.NullInt64
		if err := rows.Scan(&pending, &walltime); err != nil {
			return MetricResult{ID: "queue_wait_fraction", Computable: false, Reason: err.Error()}
		}
		denom := float64(pending.Int64)
		if walltime.Valid {
			denom += float64(walltime.Int64)
		}
		if denom <= 0 {
			continue
		}
		total += float64(pending.Int64) / denom
		n++
	}
	if n == 0 {
		return MetricResult{ID: "queue_wait_fraction", Computable: false,
			Reason: "no runs in window have pending_seconds populated yet"}
	}
	return MetricResult{ID: "queue_wait_fraction", Value: total / float64(n), Computable: true}
}

// queryActiveEngagementHours: cluster cli_audit.invoked_at into sessions
// using a 30-min idle gap; sum (last - first) per session in hours.
// Returns 0 when no audit rows in window (table exists but is empty —
// cli_audit is forward-substrate today, populated as the verb-tracing
// hook lands).
func queryActiveEngagementHours(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	where, args := auditWindowClause(opts)
	q := fmt.Sprintf(`SELECT invoked_at FROM cli_audit WHERE %s ORDER BY invoked_at`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return MetricResult{ID: "active_engagement_hours", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	const gapSec int64 = 30 * 60
	var (
		sessionStart int64
		lastTs       int64
		totalSec     int64
	)
	first := true
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return MetricResult{ID: "active_engagement_hours", Computable: false, Reason: err.Error()}
		}
		if first {
			sessionStart = ts
			lastTs = ts
			first = false
			continue
		}
		if ts-lastTs > gapSec {
			totalSec += lastTs - sessionStart
			sessionStart = ts
		}
		lastTs = ts
	}
	if !first {
		totalSec += lastTs - sessionStart
	}
	hours := float64(totalSec) / 3600.0
	return MetricResult{ID: "active_engagement_hours", Value: hours, Computable: true}
}

// querySpectatorHours: count of cli_audit rows whose verb implies the
// user is monitoring an in-flight run (job status / job logs / job watch
// / pipeline status / pipeline logs / pipeline watch) × the average
// run walltime in hours. A coarse proxy for "how long the user spent
// with their nose pressed against the glass".
func querySpectatorHours(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereAudit, argsAudit := auditWindowClause(opts)
	q1 := fmt.Sprintf(`
		SELECT COUNT(*) FROM cli_audit
		WHERE %s AND verb IN ('job status','job logs','job watch',
			'pipeline status','pipeline logs','pipeline watch',
			'module status','module logs','module watch')`, whereAudit)
	var watchN int64
	if err := db.QueryRowContext(ctx, q1, argsAudit...).Scan(&watchN); err != nil {
		return MetricResult{ID: "spectator_hours", Computable: false, Reason: err.Error()}
	}
	if watchN == 0 {
		return MetricResult{ID: "spectator_hours", Value: 0.0, Computable: true}
	}
	whereRuns, argsRuns := runWindowClause(opts)
	q2 := fmt.Sprintf(`SELECT AVG(walltime_seconds) FROM runs WHERE %s AND walltime_seconds IS NOT NULL`, whereRuns)
	var avgWalltime sql.NullFloat64
	if err := db.QueryRowContext(ctx, q2, argsRuns...).Scan(&avgWalltime); err != nil {
		return MetricResult{ID: "spectator_hours", Computable: false, Reason: err.Error()}
	}
	if !avgWalltime.Valid || avgWalltime.Float64 <= 0 {
		// No completed runs to anchor the proxy.
		return MetricResult{ID: "spectator_hours", Value: 0.0, Computable: true}
	}
	hours := float64(watchN) * (avgWalltime.Float64 / 3600.0)
	return MetricResult{ID: "spectator_hours", Value: hours, Computable: true}
}

// queryAsyncJobFraction: fraction of completed runs that have ZERO
// watch-verb cli_audit rows between submitted_at and completed_at.
// Spec abc-report.md §C fixture coverage.
func queryAsyncJobFraction(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	// Pull every completed run + its watch-verb count via a correlated
	// subquery. SQLite handles this fine on the row counts a personal
	// local.db will ever hold.
	q := fmt.Sprintf(`
		SELECT
			(SELECT COUNT(*) FROM cli_audit a
			   WHERE a.invoked_at >= r.submitted_at
			     AND a.invoked_at <= COALESCE(r.completed_at, r.submitted_at)
			     AND a.verb IN ('job status','job logs','job watch',
			                    'pipeline status','pipeline logs','pipeline watch',
			                    'module status','module logs','module watch')) AS n_watch
		FROM runs r WHERE %s AND r.status = 'completed'`, whereRuns)
	rows, err := db.QueryContext(ctx, q, argsRuns...)
	if err != nil {
		return MetricResult{ID: "async_job_fraction", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var total, async int
	for rows.Next() {
		var nWatch int64
		if err := rows.Scan(&nWatch); err != nil {
			return MetricResult{ID: "async_job_fraction", Computable: false, Reason: err.Error()}
		}
		total++
		if nWatch == 0 {
			async++
		}
	}
	if total == 0 {
		return MetricResult{ID: "async_job_fraction", Value: 0.0, Computable: true}
	}
	return MetricResult{ID: "async_job_fraction", Value: float64(async) / float64(total), Computable: true}
}

// queryCognitiveOverheadScore: distinct cli_audit verbs per completed
// run, averaged. Lightweight proxy for "tools touched per result".
func queryCognitiveOverheadScore(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT
			(SELECT COUNT(DISTINCT verb) FROM cli_audit a
			   WHERE a.invoked_at >= r.submitted_at
			     AND a.invoked_at <= COALESCE(r.completed_at, r.submitted_at)) AS n_verbs
		FROM runs r WHERE %s AND r.status = 'completed'`, whereRuns)
	rows, err := db.QueryContext(ctx, q, argsRuns...)
	if err != nil {
		return MetricResult{ID: "cognitive_overhead_score", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var sum, n int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return MetricResult{ID: "cognitive_overhead_score", Computable: false, Reason: err.Error()}
		}
		sum += v
		n++
	}
	if n == 0 {
		return MetricResult{ID: "cognitive_overhead_score", Value: 0.0, Computable: true}
	}
	return MetricResult{ID: "cognitive_overhead_score", Value: float64(sum) / float64(n), Computable: true}
}

// queryWorkflowsUnattended: count of completed runs that had ZERO
// watch-verb audit rows AND ZERO failures along the way (i.e. shipped
// a result without any user babysitting). Same shape as async_job_fraction
// but a count, scoped to first-try successes.
func queryWorkflowsUnattended(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT COUNT(*) FROM runs r
		WHERE %s AND r.status = 'completed'
		  AND (SELECT COUNT(*) FROM cli_audit a
		         WHERE a.invoked_at >= r.submitted_at
		           AND a.invoked_at <= COALESCE(r.completed_at, r.submitted_at)
		           AND a.verb IN ('job status','job logs','job watch',
		                          'pipeline status','pipeline logs','pipeline watch',
		                          'module status','module logs','module watch')) = 0`, whereRuns)
	var n int64
	if err := db.QueryRowContext(ctx, q, argsRuns...).Scan(&n); err != nil {
		return MetricResult{ID: "workflows_unattended", Computable: false, Reason: err.Error()}
	}
	return MetricResult{ID: "workflows_unattended", Value: float64(n), Computable: true}
}

// querySubmissionSource: fraction-of-runs map by submission_source bucket
// (template / handwritten / rerun / automation / unknown).
func querySubmissionSource(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	q := fmt.Sprintf(`SELECT submission_source FROM runs WHERE %s`, whereRuns)
	rows, err := db.QueryContext(ctx, q, argsRuns...)
	if err != nil {
		if isMissingColumnErr(err) {
			return MetricResult{ID: "submission_source", Computable: false,
				Reason: MissingColumnReason("0010_runs_submission_source")}
		}
		return MetricResult{ID: "submission_source", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return MetricResult{ID: "submission_source", Computable: false, Reason: err.Error()}
		}
		bucket := "unknown"
		if s.Valid && s.String != "" {
			switch {
			case strings.HasPrefix(s.String, "template:"):
				bucket = "template"
			default:
				bucket = s.String
			}
		}
		counts[bucket]++
		total++
	}
	if total == 0 {
		return MetricResult{ID: "submission_source", Value: counts, Computable: true}
	}
	return MetricResult{ID: "submission_source", Value: counts, Computable: true}
}

// queryResourceFit: avg(min(cpu_hours/walltime_hours/cpu_request,1)) —
// how close did the alloc consume to what was requested. 1.0 is perfect
// fit; lower means over-requested. Skips rows without cpu_request set.
func queryResourceFit(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT cpu_hours, walltime_seconds, cpu_request
		FROM runs WHERE %s AND cpu_request IS NOT NULL AND walltime_seconds > 0`, whereRuns)
	rows, err := db.QueryContext(ctx, q, argsRuns...)
	if err != nil {
		if isMissingColumnErr(err) {
			return MetricResult{ID: "resource_fit", Computable: false,
				Reason: MissingColumnReason("0009_runs_resource_request")}
		}
		return MetricResult{ID: "resource_fit", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	var sum float64
	var n int
	for rows.Next() {
		var cpuH sql.NullFloat64
		var wallSec sql.NullInt64
		var cpuReq sql.NullFloat64
		if err := rows.Scan(&cpuH, &wallSec, &cpuReq); err != nil {
			return MetricResult{ID: "resource_fit", Computable: false, Reason: err.Error()}
		}
		if !cpuH.Valid || !wallSec.Valid || !cpuReq.Valid || wallSec.Int64 <= 0 || cpuReq.Float64 <= 0 {
			continue
		}
		consumedCores := cpuH.Float64 / (float64(wallSec.Int64) / 3600.0)
		fit := consumedCores / cpuReq.Float64
		if fit > 1 {
			fit = 1
		}
		if fit < 0 {
			fit = 0
		}
		sum += fit
		n++
	}
	if n == 0 {
		return MetricResult{ID: "resource_fit", Computable: false,
			Reason: "no runs in window have cpu_request populated yet"}
	}
	return MetricResult{ID: "resource_fit", Value: sum / float64(n), Computable: true}
}

// queryCostPerInvestigation: cumulative cpu_hours per investigation in
// the window. Currency conversion stays in `abc accounting`; we surface
// cpu_hours-per-investigation as a placeholder until the v2 reporter
// imports the rate-card directly. Returns a map[investigation_id]hours.
func queryCostPerInvestigation(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)
	q := fmt.Sprintf(`
		SELECT COALESCE(investigation_id,'(none)'), COALESCE(SUM(cpu_hours), 0)
		FROM runs WHERE %s
		GROUP BY investigation_id`, whereRuns)
	rows, err := db.QueryContext(ctx, q, argsRuns...)
	if err != nil {
		return MetricResult{ID: "cost_per_investigation", Computable: false, Reason: err.Error()}
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var cpuh float64
		if err := rows.Scan(&id, &cpuh); err != nil {
			return MetricResult{ID: "cost_per_investigation", Computable: false, Reason: err.Error()}
		}
		out[id] = cpuh
	}
	return MetricResult{ID: "cost_per_investigation", Value: out, Computable: true}
}

// queryHoursSaved: composite. Sums:
//
//   - AutoRetrySavedMinutes      × failed→completed pairs (proxy: stabilisation_runs)
//   - SmartDefaultSavedMinutes   × runs with cpu_request populated
//   - FailureSummarySavedMinutes × failed runs
//   - TemplateReuseSavedMinutes  × runs whose submission_source starts "template:" or = "rerun"
//   - AsyncRunSpectatorAvoidedMin × async-job count (zero watch-verb audits)
func queryHoursSaved(ctx context.Context, db *sql.DB, opts QueryOptions) MetricResult {
	whereRuns, argsRuns := runWindowClause(opts)

	// Failed runs.
	var failed int64
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM runs WHERE %s AND status = 'failed'`, whereRuns), argsRuns...,
	).Scan(&failed); err != nil {
		return MetricResult{ID: "hours_saved", Computable: false, Reason: err.Error()}
	}

	// Stabilisation runs (failed-before-success), reused as the auto-retry proxy.
	stab := queryStabilisationRuns(ctx, db, opts)
	var stabRuns float64
	if v, ok := stab.Value.(float64); ok {
		stabRuns = v
	}

	// Runs with cpu_request — i.e. submitted via paths that captured it.
	var smartDefaults int64
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM runs WHERE %s AND cpu_request IS NOT NULL`, whereRuns), argsRuns...,
	).Scan(&smartDefaults); err != nil && !isMissingColumnErr(err) {
		return MetricResult{ID: "hours_saved", Computable: false, Reason: err.Error()}
	}

	// Templated / rerun submissions.
	var templated int64
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM runs
		             WHERE %s AND (submission_source LIKE 'template:%%' OR submission_source = 'rerun')`, whereRuns),
		argsRuns...,
	).Scan(&templated); err != nil && !isMissingColumnErr(err) {
		return MetricResult{ID: "hours_saved", Computable: false, Reason: err.Error()}
	}

	// Async runs (re-use the same shape the dedicated metric uses).
	asyncRes := queryAsyncJobFraction(ctx, db, opts)
	asyncFraction := 0.0
	if v, ok := asyncRes.Value.(float64); ok {
		asyncFraction = v
	}
	var totalCompleted int64
	_ = db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM runs WHERE %s AND status = 'completed'`, whereRuns), argsRuns...,
	).Scan(&totalCompleted)
	asyncCount := math.Round(asyncFraction * float64(totalCompleted))

	totalMinutes := float64(int64(stabRuns)*AutoRetrySavedMinutes) +
		float64(smartDefaults*SmartDefaultSavedMinutes) +
		float64(failed*FailureSummarySavedMinutes) +
		float64(templated*TemplateReuseSavedMinutes) +
		asyncCount*float64(AsyncRunSpectatorAvoidedMin)

	return MetricResult{ID: "hours_saved", Value: totalMinutes / 60.0, Computable: true}
}

// isMissingColumnErr reports whether err is a SQLite "no such column"
// error. Used by the queries that depend on migrations 0008/0009/0010 —
// when a v2 binary runs against a v1 DB, those queries surface a
// targeted "run abc localdb migrate" reason rather than crashing.
// Spec abc-report.md §F.
func isMissingColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such column") || strings.Contains(msg, "no column")
}
