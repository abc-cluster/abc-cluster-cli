package report

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// TestQueryRuns_JobsFirstAndPipelinePending exercises the two
// load-bearing invariants of `abc report runs`:
//  1. Jobs sort BEFORE pipelines (verb ASC; 'job' < 'pipeline')
//  2. Pipeline rows arrive with CostPending=true so the renderer
//     knows to emit "—" instead of a misleading head-only number.
func TestQueryRuns_JobsFirstAndPipelinePending(t *testing.T) {
	db := openFixtureDB(t)
	// Insert a pipeline submitted EARLIER and a job submitted LATER.
	// Pure submitted_at DESC would put the job above the pipeline; the
	// jobs-first invariant is a verb-ASC pre-sort, so even if the
	// pipeline were submitted later we'd still see jobs first.
	insertRun(t, db, "pipe1", "completed", 1700000000, 1700003600, 3600, 1.0)
	updateVerbAndWorkload(t, db, "pipe1", "pipeline", "nf-core/sarek")

	insertRun(t, db, "job1", "completed", 1700100000, 1700100600, 600, 0.5)
	updateVerbAndWorkload(t, db, "job1", "job", "samtools sort")

	res, err := QueryRuns(context.Background(), db, RunsQuery{
		ContextName: "ctx",
		Since:       time.Unix(0, 0),
		Until:       time.Unix(2000000000, 0),
	}, acct.ZADefaults())
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("Total = %d, want 2", res.Total)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("Rows = %d, want 2", len(res.Rows))
	}
	if res.Rows[0].Verb != "job" {
		t.Errorf("rows[0].Verb = %q, want 'job' (jobs first invariant)", res.Rows[0].Verb)
	}
	if res.Rows[1].Verb != "pipeline" {
		t.Errorf("rows[1].Verb = %q, want 'pipeline'", res.Rows[1].Verb)
	}
	if res.Rows[0].CostPending {
		t.Error("job row CostPending should be false; honest cost expected")
	}
	if !res.Rows[1].CostPending {
		t.Error("pipeline row CostPending should be true (head-only undercount)")
	}
}

// TestQueryRuns_VerbFilter: --verb=job filters to job rows only.
func TestQueryRuns_VerbFilter(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "p1", "completed", 1700000000, 1700003600, 3600, 1.0)
	updateVerbAndWorkload(t, db, "p1", "pipeline", "nf-core/demo")
	insertRun(t, db, "j1", "completed", 1700000100, 1700000200, 100, 0.1)
	updateVerbAndWorkload(t, db, "j1", "job", "hello")

	res, err := QueryRuns(context.Background(), db, RunsQuery{
		ContextName: "ctx",
		Verb:        "job",
		Since:       time.Unix(0, 0),
		Until:       time.Unix(2000000000, 0),
	}, acct.ZADefaults())
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if res.Total != 1 || len(res.Rows) != 1 {
		t.Fatalf("--verb=job: Total=%d Rows=%d, want 1/1", res.Total, len(res.Rows))
	}
	if res.Rows[0].Verb != "job" {
		t.Errorf("row.Verb = %q, want 'job'", res.Rows[0].Verb)
	}
}

// TestQueryRuns_LimitAndCount: Total reflects unfiltered count;
// the Rows slice respects --limit.
func TestQueryRuns_LimitAndCount(t *testing.T) {
	db := openFixtureDB(t)
	for i := 0; i < 5; i++ {
		id := "j" + string(rune('A'+i))
		insertRun(t, db, id, "completed", int64(1700000000+i), int64(1700003600+i), 3600, 0.5)
		updateVerbAndWorkload(t, db, id, "job", "samtools")
	}
	res, err := QueryRuns(context.Background(), db, RunsQuery{
		ContextName: "ctx",
		Limit:       2,
		Since:       time.Unix(0, 0),
		Until:       time.Unix(2000000000, 0),
	}, acct.ZADefaults())
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if res.Total != 5 {
		t.Errorf("Total = %d, want 5 (count is pre-limit)", res.Total)
	}
	if len(res.Rows) != 2 {
		t.Errorf("len(Rows) = %d, want 2 (limit applied)", len(res.Rows))
	}
}

// TestRenderRunsText_PipelineFootnote: the footnote about pipeline
// pending cost must appear when at least one pipeline row is in the
// output, and must NOT appear when only jobs are listed.
func TestRenderRunsText_PipelineFootnote(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	pipelineRow := RunRow{
		Verb: "pipeline", WorkloadRef: "nf-core/sarek", Status: "completed",
		SubmittedAt: now, CPUHours: 0.4, CostPending: true,
	}
	jobRow := RunRow{
		Verb: "job", WorkloadRef: "samtools sort", Status: "completed",
		SubmittedAt: now, CPUHours: 2.0, CostZAR: 1.0, EmissionsKgCO2e: 0.1,
	}

	var sb strings.Builder
	if err := RenderRunsText(&sb, []RunRow{jobRow, pipelineRow}, RunsTextOptions{}); err != nil {
		t.Fatalf("RenderRunsText: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "Pipeline cost/emissions are pending") {
		t.Errorf("missing pipeline-pending footnote in mixed output:\n%s", out)
	}
	// Pipeline cell renders as em-dash.
	if !strings.Contains(out, "    —    ") && !strings.Contains(out, "—") {
		t.Errorf("missing em-dash for pipeline cost cell:\n%s", out)
	}

	// Jobs-only output should NOT carry the footnote.
	sb.Reset()
	if err := RenderRunsText(&sb, []RunRow{jobRow}, RunsTextOptions{}); err != nil {
		t.Fatalf("RenderRunsText (jobs-only): %v", err)
	}
	if strings.Contains(sb.String(), "Pipeline cost/emissions are pending") {
		t.Errorf("jobs-only output should NOT carry the pipeline footnote:\n%s", sb.String())
	}
}

// TestRenderRunsText_ShowingNofM: the footer hints at narrowing the
// window when Total > len(Rows).
func TestRenderRunsText_ShowingNofM(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	rows := []RunRow{{Verb: "job", WorkloadRef: "x", Status: "completed", SubmittedAt: now}}
	var sb strings.Builder
	if err := RenderRunsText(&sb, rows, RunsTextOptions{Total: 250}); err != nil {
		t.Fatalf("RenderRunsText: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "Showing 1 of 250 runs") {
		t.Errorf("missing 'Showing N of M' footer:\n%s", out)
	}
	if !strings.Contains(out, "--since") || !strings.Contains(out, "--limit") {
		t.Errorf("missing filter hint in footer:\n%s", out)
	}
}

// updateVerbAndWorkload — the existing insertRun helper writes
// pipeline rows with a default workload_ref; we override here so each
// test case can set the verb / ref it needs without complicating the
// shared fixture helper.
func updateVerbAndWorkload(t *testing.T, db *sql.DB, runID, verb, workloadRef string) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE runs SET verb = ?, workload_ref = ? WHERE run_id = ?`,
		verb, workloadRef, runID); err != nil {
		t.Fatal(err)
	}
}
