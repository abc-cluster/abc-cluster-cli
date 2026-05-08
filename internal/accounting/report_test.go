package accounting

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	_ "modernc.org/sqlite"
)

// openTestDB opens a fresh DB and runs migrations.
func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrations.Apply(db, path, "v0.1.25-test"); err != nil {
		t.Fatalf("Apply migrations: %v", err)
	}
	return db, path
}

// seedRun inserts a completed run row directly. Bypasses InsertRun /
// CompleteRun so the test can fully control the values. Foreign keys are
// disabled at the seed point — the cost / emissions queries don't depend
// on those rows existing, only on what's stored in `runs`.
func seedRun(t *testing.T, db *sql.DB, ctxName, ns, projectID, invID, pipeline, status string, submittedAt, completedAt int64, cpuHours, memGBHours float64, gpuCount int, walltimeSec int64) {
	t.Helper()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	var compl any
	if completedAt > 0 {
		compl = completedAt
	}
	var gpu any
	if gpuCount > 0 {
		gpu = gpuCount
	}
	var pid, iid any
	if projectID != "" {
		pid = projectID
	}
	if invID != "" {
		iid = invID
	}
	runID := newRunIDFor(t, pipeline, ns, status, submittedAt)
	_, err := db.Exec(`INSERT INTO runs
		(run_id, context_name, project_id, investigation_id, verb, workload_ref, namespace,
		 submitted_at, completed_at, status, cpu_hours, memory_gb_hours, walltime_seconds, gpu_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, ctxName, pid, iid, "pipeline", pipeline, ns,
		submittedAt, compl, status, cpuHours, memGBHours, walltimeSec, gpu, submittedAt)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

var seedSeq int64

func newRunIDFor(t *testing.T, pipeline, ns, status string, submittedAt int64) string {
	seedSeq++
	return "run-" + pipeline + "-" + ns + "-" + status + "-" + sprintInt(submittedAt) + "-" + sprintInt(seedSeq)
}

func sprintInt(i int64) string {
	return time.Unix(i, 0).Format("20060102150405") + "x"
}

func TestAggregate_AccountingByNamespace(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()

	// Two completed runs in namespace "su-mbhg-bio", one in "su-mbhg-host".
	seedRun(t, db, "ctx", "su-mbhg-bio", "p1", "i1", "nf-rnaseq", "completed", now-1000, now-500, 10.0, 20.0, 0, 500)
	seedRun(t, db, "ctx", "su-mbhg-bio", "p1", "i1", "nf-rnaseq", "completed", now-2000, now-1500, 5.0, 10.0, 0, 500)
	seedRun(t, db, "ctx", "su-mbhg-host", "p2", "i2", "nf-rnaseq", "completed", now-1000, now-500, 4.0, 8.0, 0, 500)

	card, err := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Aggregate(context.Background(), db, ReportOptions{
		Mode:        ModeAccounting,
		By:          GroupByNamespace,
		Since:       time.Unix(now-3000, 0),
		Until:       time.Unix(now, 0),
		ContextName: "ctx",
	}, card)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rep.Rows))
	}
	got := map[string]float64{}
	for _, r := range rep.Rows {
		got[r.Group] = r.Total
	}
	// bio: cpu_hours=15 → 15*0.50 + memGB=30 → 30*0.05 = 7.5 + 1.5 = 9.0 ZAR
	want := 9.0
	if approx(got["su-mbhg-bio"], want, 0.01) == false {
		t.Errorf("su-mbhg-bio total = %v, want %v", got["su-mbhg-bio"], want)
	}
	// host: 4*0.5 + 8*0.05 = 2 + 0.4 = 2.4 ZAR
	if approx(got["su-mbhg-host"], 2.4, 0.01) == false {
		t.Errorf("su-mbhg-host total = %v, want 2.4", got["su-mbhg-host"])
	}
}

func TestAggregate_EmissionsFormula(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	seedRun(t, db, "ctx", "ns1", "", "", "nf", "completed", now-1000, now-500, 10.0, 20.0, 0, 500)

	card, err := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Aggregate(context.Background(), db, ReportOptions{
		Mode:          ModeEmissions,
		By:            GroupByNamespace,
		Since:         time.Unix(now-3000, 0),
		Until:         time.Unix(now, 0),
		ContextName:   "ctx",
		EmissionsUnit: UnitKg,
	}, card)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rep.Rows))
	}
	// Expected: energy = ((10*12 + 20*0.3725) / 1000) * 1.5
	//                 = ((120 + 7.45) / 1000) * 1.5 = 0.12745 * 1.5 = 0.191175 kWh
	// CO2_kg = 0.191175 * 900 / 1000 = 0.1720575 kg
	want := 0.1720575
	if approx(rep.Rows[0].Total, want, 0.001) == false {
		t.Errorf("emissions total = %v, want ~%v", rep.Rows[0].Total, want)
	}
}

func TestAggregate_IncludeIncomplete(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	// One completed run, one running.
	seedRun(t, db, "ctx", "ns", "", "", "nf", "completed", now-1000, now-500, 1.0, 1.0, 0, 500)
	seedRun(t, db, "ctx", "ns", "", "", "nf", "running", now-100, 0, 0.0, 0.0, 0, 0)

	card, _ := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	// Without --include-incomplete: only completed run counts.
	rep, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-3000, 0), Until: time.Unix(now, 0),
		ContextName: "ctx",
	}, card)
	if len(rep.Rows) != 1 || rep.Rows[0].Runs != 1 {
		t.Errorf("default: expected 1 row with 1 run, got %+v", rep.Rows)
	}
	// With flag: includes both rows.
	rep2, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-3000, 0), Until: time.Unix(now, 0),
		ContextName: "ctx", IncludeIncomplete: true,
	}, card)
	if len(rep2.Rows) != 1 || rep2.Rows[0].Runs != 2 {
		t.Errorf("with flag: expected 1 row with 2 runs, got %+v", rep2.Rows)
	}
}

func TestRender_TableIncludesRateCardFooter(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	seedRun(t, db, "ctx", "ns", "", "", "nf", "completed", now-1000, now-500, 1.0, 1.0, 0, 500)
	card, _ := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	rep, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-3000, 0), Until: time.Unix(now, 0),
		ContextName: "ctx",
	}, card)

	var sb strings.Builder
	if err := Render(&sb, rep, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Output: OutputTable, RateSource: RateSourceFull,
	}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		"Namespace", "Cost (ZAR)", "ns",
		"Rate card (effective):",
		"cost.cpu_hour",
		"built-in",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRender_CSVNoFooter(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	seedRun(t, db, "ctx", "ns", "", "", "nf", "completed", now-1000, now-500, 1.0, 1.0, 0, 500)
	card, _ := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	rep, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-3000, 0), Until: time.Unix(now, 0),
		ContextName: "ctx",
	}, card)

	var sb strings.Builder
	if err := Render(&sb, rep, ReportOptions{
		Output: OutputCSV, RateSource: RateSourceNone,
	}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if strings.Contains(out, "Rate card") {
		t.Errorf("CSV output should not contain rate-card footer:\n%s", out)
	}
	if !strings.HasPrefix(out, "namespace,total,") {
		t.Errorf("CSV output missing header: %q", out)
	}
}

func TestRender_JSONIncludesRateCard(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	seedRun(t, db, "ctx", "ns", "", "", "nf", "completed", now-1000, now-500, 1.0, 1.0, 0, 500)
	card, _ := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	rep, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-3000, 0), Until: time.Unix(now, 0),
		ContextName: "ctx",
	}, card)
	var sb strings.Builder
	if err := Render(&sb, rep, ReportOptions{Output: OutputJSON}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "\"rate_card\"") {
		t.Errorf("JSON output missing rate_card key:\n%s", out)
	}
}

func TestAggregate_GpuHoursDerived(t *testing.T) {
	db, _ := openTestDB(t)
	now := time.Now().Unix()
	// 2 GPUs * 1 hour walltime = 2 GPU·hours; 0 cpu / mem to isolate.
	seedRun(t, db, "ctx", "ns", "", "", "nf", "completed", now-3700, now-100, 0, 0, 2, 3600)
	card, _ := Resolve(ZADefaults(), LayeredOverrides{}, FlagOverrides{})
	rep, _ := Aggregate(context.Background(), db, ReportOptions{
		Mode: ModeAccounting, By: GroupByNamespace,
		Since: time.Unix(now-9999, 0), Until: time.Unix(now, 0),
		ContextName: "ctx",
	}, card)
	if len(rep.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", rep.Rows)
	}
	// 2 GPU·h * 9.00 ZAR = 18.00 ZAR
	if approx(rep.Rows[0].Total, 18.0, 0.01) == false {
		t.Errorf("expected 18.0, got %v", rep.Rows[0].Total)
	}
	if approx(rep.Rows[0].GpuHours, 2.0, 0.01) == false {
		t.Errorf("expected gpu_hours=2.0, got %v", rep.Rows[0].GpuHours)
	}
}

func approx(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
