package report

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	_ "modernc.org/sqlite"
)

// openFixtureDB opens a fresh SQLite at a temp path with the full
// embedded migration set applied.
func openFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrations.Apply(db, path, "v0-test"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return db
}

// fullWindow is an inclusive [1970, 2100] window so all fixture rows
// land inside it without per-test arithmetic.
var fullWindow = Window{
	Since: time.Unix(0, 0),
	Until: time.Unix(4102444800, 0), // 2100-01-01
}

func insertRun(t *testing.T, db *sql.DB, runID, status string, submittedAt int64, completedAt int64, walltime int64, cpuHours float64, opts ...runOpt) {
	t.Helper()
	r := runRow{
		runID: runID, status: status, submittedAt: submittedAt,
		completedAt: completedAt, walltime: walltime, cpuHours: cpuHours,
		workloadRef: "wf",
	}
	for _, o := range opts {
		o(&r)
	}
	_, err := db.Exec(`
		INSERT INTO runs (run_id, context_name, verb, workload_ref, submitted_at, completed_at,
		                  status, cpu_hours, walltime_seconds, gpu_count,
		                  pending_seconds, cpu_request, mem_request_gb, submission_source,
		                  investigation_id, created_at)
		VALUES (?, 'ctx', 'pipeline', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.runID, r.workloadRef, r.submittedAt,
		nullableInt(r.completedAt), r.status, r.cpuHours, nullableInt(r.walltime), nullableInt(int64(r.gpuCount)),
		nullableInt(r.pendingSeconds), nullableF(r.cpuRequest), nullableF(r.memRequestGB),
		nullableString(r.submissionSource), nullableString(r.investigationID),
		r.submittedAt)
	if err != nil {
		t.Fatalf("insert run %s: %v", runID, err)
	}
}

func insertAudit(t *testing.T, db *sql.DB, ts int64, verb string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO cli_audit (invoked_at, context_name, verb) VALUES (?, 'ctx', ?)`, ts, verb)
	if err != nil {
		t.Fatalf("insert audit: %v", err)
	}
}

type runRow struct {
	runID, workloadRef, status, submissionSource, investigationID string
	submittedAt, completedAt, walltime, pendingSeconds            int64
	cpuHours, cpuRequest, memRequestGB                            float64
	gpuCount                                                      int
}

type runOpt func(*runRow)

func withWorkload(ref string) runOpt           { return func(r *runRow) { r.workloadRef = ref } }
func withPending(s int64) runOpt               { return func(r *runRow) { r.pendingSeconds = s } }
func withCPURequest(v float64) runOpt          { return func(r *runRow) { r.cpuRequest = v } }
func withMemRequest(v float64) runOpt          { return func(r *runRow) { r.memRequestGB = v } }
func withSubSource(s string) runOpt            { return func(r *runRow) { r.submissionSource = s } }
func withInvestigation(s string) runOpt        { return func(r *runRow) { r.investigationID = s } }
func withGPUCount(n int) runOpt                { return func(r *runRow) { r.gpuCount = n } }

func nullableInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func nullableF(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TestCompute_EmptyDB: every metric returns gracefully (no panic), with
// values of 0 (or empty-map equivalent) and Computable=true except
// queue_wait_fraction / resource_fit which need real rows.
func TestCompute_EmptyDB(t *testing.T) {
	db := openFixtureDB(t)
	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	if len(res) != len(Metrics) {
		t.Fatalf("got %d results, want %d", len(res), len(Metrics))
	}
	for _, r := range res {
		if r.ID == "queue_wait_fraction" || r.ID == "resource_fit" {
			if r.Computable {
				t.Errorf("%s on empty DB: expected Computable=false (no rows)", r.ID)
			}
			continue
		}
		if !r.Computable {
			t.Errorf("%s on empty DB: expected Computable=true, got %s", r.ID, r.Reason)
		}
	}
}

// TestRetryDepth_FirstTrySuccess: a single workload that succeeded on
// first try → retry_depth=1, mttfs=walltime/3600.
func TestRetryDepth_FirstTrySuccess(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	rd, _ := ResultByID(res, "retry_depth")
	if v, _ := rd.Value.(float64); v != 1.0 {
		t.Errorf("retry_depth = %v, want 1.0", rd.Value)
	}
	mt, _ := ResultByID(res, "mttfs")
	if v, _ := mt.Value.(float64); v != 1.0 { // 1 hour
		t.Errorf("mttfs = %v, want 1.0", mt.Value)
	}
}

// TestRetryDepth_ThreeRetriesBeforeSuccess: a workload with three
// failures before success → retry_depth=4.
func TestRetryDepth_ThreeRetriesBeforeSuccess(t *testing.T) {
	db := openFixtureDB(t)
	wf := withWorkload("recipe-A")
	insertRun(t, db, "r1", "failed",    1700000000, 1700001000, 1000, 0.3, wf)
	insertRun(t, db, "r2", "failed",    1700002000, 1700003000, 1000, 0.3, wf)
	insertRun(t, db, "r3", "failed",    1700004000, 1700005000, 1000, 0.3, wf)
	insertRun(t, db, "r4", "completed", 1700006000, 1700009600, 3600, 1.0, wf)

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	rd, _ := ResultByID(res, "retry_depth")
	if v, _ := rd.Value.(float64); v != 4.0 {
		t.Errorf("retry_depth = %v, want 4.0", rd.Value)
	}
	stab, _ := ResultByID(res, "stabilisation_runs")
	if v, _ := stab.Value.(float64); v != 3.0 {
		t.Errorf("stabilisation_runs = %v, want 3.0", stab.Value)
	}
	// mttfs spans submitted_at(r1) → completed_at(r4) = 9600s = 8/3 hr.
	mt, _ := ResultByID(res, "mttfs")
	got, _ := mt.Value.(float64)
	want := 9600.0 / 3600.0
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("mttfs = %v, want ~%v", got, want)
	}
}

// TestQueueWaitFraction_NullPendingPrintsReason: when no row in the
// window has pending_seconds populated, the metric is non-computable
// with a clear reason, NOT silently zero.
func TestQueueWaitFraction_NullPendingPrintsReason(t *testing.T) {
	db := openFixtureDB(t)
	// Insert a run with NO pending_seconds.
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	q, _ := ResultByID(res, "queue_wait_fraction")
	if q.Computable {
		t.Errorf("queue_wait_fraction: want Computable=false on NULL row, got %v (value=%v)", q.Computable, q.Value)
	}
	if q.Reason == "" {
		t.Errorf("queue_wait_fraction: want non-empty Reason")
	}
}

// TestQueueWaitFraction_WithPendingPopulated: when at least one row has
// pending_seconds, the metric computes the average wait fraction.
func TestQueueWaitFraction_WithPendingPopulated(t *testing.T) {
	db := openFixtureDB(t)
	// pending=300s, walltime=900s → fraction = 300/(300+900) = 0.25
	insertRun(t, db, "r1", "completed", 1700000000, 1700001200, 900, 0.25, withPending(300))
	// pending=60s, walltime=3540s → fraction = 60/3600 ≈ 0.0167
	insertRun(t, db, "r2", "completed", 1700100000, 1700103600, 3540, 1.0, withPending(60))

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	q, _ := ResultByID(res, "queue_wait_fraction")
	if !q.Computable {
		t.Fatalf("queue_wait_fraction: want Computable=true, got reason=%s", q.Reason)
	}
	got, _ := q.Value.(float64)
	want := (0.25 + (60.0/3600.0)) / 2.0
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("queue_wait_fraction = %v, want %v", got, want)
	}
}

// TestAsyncJobFraction_NoWatchVerbs: a completed run with zero watch
// verbs in [submitted_at, completed_at] counts as async. Spec §C
// fixture coverage.
func TestAsyncJobFraction_NoWatchVerbs(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "async", "completed", 1700000000, 1700003600, 3600, 1.0)
	insertRun(t, db, "watched", "completed", 1700100000, 1700103600, 3600, 1.0)
	// "watched" has watch verbs in its window.
	insertAudit(t, db, 1700101000, "job status")
	insertAudit(t, db, 1700102000, "job logs")

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	a, _ := ResultByID(res, "async_job_fraction")
	got, _ := a.Value.(float64)
	want := 0.5
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("async_job_fraction = %v, want %v", got, want)
	}
}

// TestActiveEngagementHours_GapClustering: cli_audit rows clustered into
// sessions by 30-min idle gap; each session contributes (last-first)/3600.
func TestActiveEngagementHours_GapClustering(t *testing.T) {
	db := openFixtureDB(t)
	// Session 1: 09:00 → 09:20 (1200s)
	insertAudit(t, db, 1700000000, "pipeline run")
	insertAudit(t, db, 1700000600, "pipeline status")
	insertAudit(t, db, 1700001200, "pipeline logs")
	// Gap of 2 hr → session boundary.
	// Session 2: 11:20 → 11:30 (600s)
	insertAudit(t, db, 1700008400, "job run")
	insertAudit(t, db, 1700009000, "job status")

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	r, _ := ResultByID(res, "active_engagement_hours")
	got, _ := r.Value.(float64)
	want := (1200.0 + 600.0) / 3600.0
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("active_engagement_hours = %v, want %v", got, want)
	}
}

// TestSubmissionSourceBuckets: bucketing classifies the four source
// kinds and surfaces "unknown" for NULL rows.
func TestSubmissionSourceBuckets(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700001000, 1000, 1.0, withSubSource("template:nf-core/rnaseq"))
	insertRun(t, db, "r2", "completed", 1700002000, 1700003000, 1000, 1.0, withSubSource("handwritten"))
	insertRun(t, db, "r3", "completed", 1700004000, 1700005000, 1000, 1.0, withSubSource("rerun"))
	insertRun(t, db, "r4", "completed", 1700006000, 1700007000, 1000, 1.0) // NULL

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	r, _ := ResultByID(res, "submission_source")
	m, ok := r.Value.(map[string]int)
	if !ok {
		t.Fatalf("submission_source value type %T, want map[string]int", r.Value)
	}
	if m["template"] != 1 || m["handwritten"] != 1 || m["rerun"] != 1 || m["unknown"] != 1 {
		t.Errorf("submission_source buckets = %v, want one of each", m)
	}
}

// TestHoursSaved_Headline: with concrete fixture matching the
// §"Acceptance" sample (auto-retry + failure-summary + template-reuse
// contributions), hours_saved is the sum of all enabled heuristics.
func TestHoursSaved_Headline(t *testing.T) {
	db := openFixtureDB(t)
	// 2 stabilisation_runs (auto-retry proxy): 2 × 15 = 30 min
	wfA := withWorkload("A")
	insertRun(t, db, "a1", "failed",    1700000000, 1700001000, 1000, 0.3, wfA)
	insertRun(t, db, "a2", "failed",    1700002000, 1700003000, 1000, 0.3, wfA)
	insertRun(t, db, "a3", "completed", 1700004000, 1700005000, 1000, 0.3, wfA)
	// Failure-summary: 2 failed × 30 = 60 min.
	// Template reuse: 1 templated × 60 = 60 min.
	insertRun(t, db, "t1", "completed", 1700100000, 1700101000, 1000, 0.3, withSubSource("template:nf-core/rnaseq"))
	// Smart defaults: 1 row with cpu_request × 10 = 10 min.
	insertRun(t, db, "s1", "completed", 1700200000, 1700201000, 1000, 0.3, withCPURequest(2.0))

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	hs, _ := ResultByID(res, "hours_saved")
	got, _ := hs.Value.(float64)

	// 30 + 60 + 60 + 10 = 160 min; async runs all have zero watch verbs
	// so they each contribute 30 min apiece. There are 3 completed runs
	// with no audits → async fraction = 1.0 → asyncCount = 3 → +90 min.
	// 160 + 90 = 250 min = 4.17 hr.
	wantMin := 250.0
	want := wantMin / 60.0
	if got < want-0.05 || got > want+0.05 {
		t.Errorf("hours_saved = %v, want %v (±0.05)", got, want)
	}
}
