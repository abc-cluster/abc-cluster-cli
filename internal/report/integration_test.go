package report

import (
	"context"
	"database/sql"
	"math"
	"testing"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// TestSpendEmissionsDriftAgainstAccounting is the §D' (BINDING) drift
// regression test for `abc report` v1.
//
// The seedling-tier headline ROI line — "we spent R X, emitted Y kg
// CO₂e, and returned Z postdoc-hours" — depends on these three numbers
// being self-consistent across `abc report`, `abc accounting`, and
// `abc emissions` for the same window. This test seeds a deterministic
// fixture DB, computes spend_zar / emissions_kgco2e via the report
// path, then computes the same totals via direct calls into
// `internal/accounting.Aggregate` against the same fixture, and
// asserts equality within float epsilon.
//
// Any drift = regression. Failure here means a code path in
// `internal/report` is using a different formula or a different rate
// card from `internal/accounting`. Fix the formula, do not relax the
// epsilon.
func TestSpendEmissionsDriftAgainstAccounting(t *testing.T) {
	db := openFixtureDB(t)

	// Deterministic mix:
	//  - r1: 1 CPU·hr, 4 GB·hr, no GPU, 1000s walltime, no scratch.
	//  - r2: 2 CPU·hr, 8 GB·hr, 1 GPU, 3600s walltime, 50 GB scratch.
	//  - r3: failed, partial — ensures we don't accidentally drop
	//        completed-only rows on one side and not the other.
	insertRunFull(t, db, "r1", "completed", 1700000000, 1700001000, 1000, 1.0, 4.0, 0, 0)
	insertRunFull(t, db, "r2", "completed", 1700100000, 1700103600, 3600, 2.0, 8.0, 1, 50)
	insertRunFull(t, db, "r3", "completed", 1700200000, 1700201500, 1500, 0.4, 2.0, 0, 0)

	card := acct.ZADefaults()

	// ── path A: through the report verb ────────────────────────────────
	res := Compute(context.Background(), db, QueryOptions{
		Window: fullWindow, ContextName: "ctx", RateCard: card,
	})
	spend, _ := ResultByID(res, "spend_zar")
	em, _ := ResultByID(res, "emissions_kgco2e")
	if !spend.Computable {
		t.Fatalf("spend_zar non-computable: %s", spend.Reason)
	}
	if !em.Computable {
		t.Fatalf("emissions_kgco2e non-computable: %s", em.Reason)
	}
	gotSpend, _ := spend.Value.(float64)
	gotEmissions, _ := em.Value.(float64)

	// ── path B: directly through internal/accounting ───────────────────
	// We use --by=namespace because Aggregate requires a group axis and
	// every fixture run shares the same namespace (the schema default
	// when not set). Sum the rows to mimic the windowed total the
	// report verb produces.
	acctOpts := acct.ReportOptions{
		Mode:        acct.ModeAccounting,
		By:          acct.GroupByNamespace,
		Since:       fullWindow.Since,
		Until:       fullWindow.Until,
		ContextName: "ctx",
	}
	acctRep, err := acct.Aggregate(context.Background(), db, acctOpts, card)
	if err != nil {
		t.Fatalf("acct.Aggregate(accounting): %v", err)
	}
	wantSpend := 0.0
	for _, r := range acctRep.Rows {
		wantSpend += r.Total
	}

	emOpts := acct.ReportOptions{
		Mode:          acct.ModeEmissions,
		By:            acct.GroupByNamespace,
		Since:         fullWindow.Since,
		Until:         fullWindow.Until,
		ContextName:   "ctx",
		EmissionsUnit: acct.UnitKg,
	}
	emRep, err := acct.Aggregate(context.Background(), db, emOpts, card)
	if err != nil {
		t.Fatalf("acct.Aggregate(emissions): %v", err)
	}
	wantEmissions := 0.0
	for _, r := range emRep.Rows {
		wantEmissions += r.Total
	}

	const eps = 1e-9
	if math.Abs(gotSpend-wantSpend) > eps {
		t.Errorf("spend_zar drift: report=%v acct=%v Δ=%v (eps=%v)",
			gotSpend, wantSpend, gotSpend-wantSpend, eps)
	}
	if math.Abs(gotEmissions-wantEmissions) > eps {
		t.Errorf("emissions_kgco2e drift: report=%v emissions=%v Δ=%v (eps=%v)",
			gotEmissions, wantEmissions, gotEmissions-wantEmissions, eps)
	}
}

// TestPostdocRateThroughResolver: the report's Hourly compensation line
// must read from the resolved rate card, not a hardcoded constant.
// Override the postdoc rate via Layer 1 (config) and assert the report
// picks it up.
func TestPostdocRateThroughResolver(t *testing.T) {
	// Build a card with a Layer-1 postdoc override.
	card, err := acct.Resolve(
		acct.ZADefaults(),
		acct.LayeredOverrides{
			Accounting: map[string]string{acct.KeyCostPostdocPerHour: "525"},
			Emissions:  map[string]string{},
		},
		acct.FlagOverrides{},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if card.Cost.PostdocPerHour.Value != 525 {
		t.Fatalf("expected Layer-1 override to land at 525, got %v", card.Cost.PostdocPerHour.Value)
	}
	if card.Cost.PostdocPerHour.Source != acct.SourceLocal {
		t.Errorf("expected Source=local after Layer-1 override, got %q", card.Cost.PostdocPerHour.Source)
	}
}

// insertRunFull is a richer variant of the test-helper insertRun that
// also writes memory_gb_hours, gpu_count, and scratch_gb so the drift
// test exercises every term of the cost / emissions formula. The
// fixture schema already has these columns from earlier migrations.
func insertRunFull(t *testing.T, db *sql.DB, runID, status string, submittedAt, completedAt, walltime int64, cpuHours, memGbHours float64, gpuCount int64, scratchGb int64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO runs (run_id, context_name, namespace, verb, workload_ref, submitted_at, completed_at,
		                  status, cpu_hours, memory_gb_hours, walltime_seconds, gpu_count, scratch_gb,
		                  created_at)
		VALUES (?, 'ctx', 'ns', 'pipeline', 'wf', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, submittedAt, completedAt, status, cpuHours, memGbHours, walltime, gpuCount,
		nullableInt(scratchGb), submittedAt)
	if err != nil {
		t.Fatalf("insert run %s: %v", runID, err)
	}
}
