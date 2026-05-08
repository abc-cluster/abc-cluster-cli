package migrations

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB opens a fresh SQLite at the given path with the same PRAGMAs
// state.OpenAt() applies, but WITHOUT calling Apply() — so tests can pre-seed
// the schema_migrations table to simulate edge cases.
func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestApplyFreshDB: Apply() on an empty DB runs all embedded migrations,
// records the CLI version, and produces a backup file (none for first run
// because the file did not exist before Apply) — no, the file exists at
// the time of Apply, so a backup IS written.
func TestApplyFreshDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	db := openTestDB(t, path)

	if err := Apply(db, path, "v1.2.3-test"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	applied, err := AppliedVersions(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one applied migration")
	}
	for _, a := range applied {
		if a.AppliedByCLIVersion != "v1.2.3-test" {
			t.Errorf("migration %s recorded as %q, want v1.2.3-test",
				a.Version, a.AppliedByCLIVersion)
		}
	}

	pending, err := Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("Pending after Apply: %v, want empty", pending)
	}

	future, err := Future(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("Future after Apply: %v, want empty", future)
	}

	// Backup file written.
	matches, _ := filepath.Glob(path + ".backup-pre-*")
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 backup file, got %d (%v)", len(matches), matches)
	}
}

// TestApplyIdempotent: calling Apply twice in a row is a no-op the second time.
func TestApplyIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	db := openTestDB(t, path)

	if err := Apply(db, path, "v1.2.3"); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	first, _ := AppliedVersions(db)

	if err := Apply(db, path, "v1.2.4"); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	second, _ := AppliedVersions(db)

	if len(first) != len(second) {
		t.Errorf("second Apply applied new migrations: %d → %d", len(first), len(second))
	}
	// CLI version is NOT overwritten on no-op apply.
	for _, a := range second {
		if a.AppliedByCLIVersion != "v1.2.3" {
			t.Errorf("CLI version overwritten on idempotent apply: %q", a.AppliedByCLIVersion)
		}
	}
}

// TestSchemaAhead: DB has an applied migration version that the embedded set
// does not contain. Apply() must return ErrSchemaAhead.
func TestSchemaAhead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	db := openTestDB(t, path)

	// First, apply embedded migrations normally.
	if err := Apply(db, path, "v1.0.0"); err != nil {
		t.Fatalf("baseline Apply: %v", err)
	}
	// Then forge an applied row for a future migration this binary doesn't know.
	if _, err := db.Exec(
		`INSERT INTO schema_migrations(version, applied_at, applied_by_cli_version) VALUES (?, ?, ?)`,
		"9999_future_schema_change", 1700000000, "v9.9.9",
	); err != nil {
		t.Fatalf("forge future migration: %v", err)
	}

	err := Apply(db, path, "v1.0.0")
	if err == nil {
		t.Fatal("Apply with forged future row: want error, got nil")
	}
	if !errors.Is(err, ErrSchemaAhead) {
		t.Errorf("err = %v; want ErrSchemaAhead", err)
	}
	if !strings.Contains(err.Error(), "9999_future_schema_change") {
		t.Errorf("error doesn't name the future version: %v", err)
	}

	// Future() exposes it for `abc cache status`.
	future, err := Future(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 1 || future[0] != "9999_future_schema_change" {
		t.Errorf("Future() = %v; want [9999_future_schema_change]", future)
	}
}

// TestBackupSkippedWhenPathEmpty: in-memory or test scenarios that don't want
// a backup pass an empty path.
func TestBackupSkippedWhenPathEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	db := openTestDB(t, path)

	if err := Apply(db, "", "v1.0.0"); err != nil {
		t.Fatalf("Apply with empty path: %v", err)
	}
	matches, _ := filepath.Glob(path + ".backup-pre-*")
	if len(matches) != 0 {
		t.Errorf("backup written despite empty path: %v", matches)
	}
}

// TestBackupPruning: more than maxBackups backups exist; older ones are removed.
func TestBackupPruning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	// Touch the DB so backup() has a source to copy.
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create maxBackups + 2 fake backups with staggered mtimes.
	for i := 0; i < maxBackups+2; i++ {
		name := filepath.Join(dir, "local.db.backup-pre-0001_initial-"+string(rune('a'+i)))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := pruneBackups(path, maxBackups); err != nil {
		t.Fatalf("prune: %v", err)
	}
	matches, _ := filepath.Glob(path + ".backup-pre-*")
	if len(matches) > maxBackups {
		t.Errorf("after prune: %d backups, want ≤ %d", len(matches), maxBackups)
	}
}

// TestRunsReportColumns_Writeable: 0008/0009/0010 add new nullable columns
// to runs. After Apply() they must be writeable + readable + NULL-tolerant.
// Each column is exercised once with a concrete value and once left NULL.
func TestRunsReportColumns_Writeable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.db")
	db := openTestDB(t, path)
	if err := Apply(db, path, "v0-test"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Insert a row with all three new columns set.
	if _, err := db.Exec(`
		INSERT INTO runs (run_id, context_name, verb, workload_ref, submitted_at, status,
		                  pending_seconds, cpu_request, mem_request_gb, submission_source,
		                  created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"r-set", "ctx", "pipeline", "wf", 1700000000, "running",
		42, 4.0, 16.0, "template:nf-core/rnaseq", 1700000000); err != nil {
		t.Fatalf("insert with values: %v", err)
	}
	// Insert a row leaving the new columns NULL.
	if _, err := db.Exec(`
		INSERT INTO runs (run_id, context_name, verb, workload_ref, submitted_at, status,
		                  created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"r-null", "ctx", "pipeline", "wf", 1700000001, "running", 1700000001); err != nil {
		t.Fatalf("insert with NULLs: %v", err)
	}

	// Read both back.
	var (
		ps   sql.NullInt64
		cpu  sql.NullFloat64
		mem  sql.NullFloat64
		src  sql.NullString
	)
	if err := db.QueryRow(`SELECT pending_seconds, cpu_request, mem_request_gb, submission_source
	                         FROM runs WHERE run_id = ?`, "r-set").Scan(&ps, &cpu, &mem, &src); err != nil {
		t.Fatalf("read r-set: %v", err)
	}
	if !ps.Valid || ps.Int64 != 42 {
		t.Errorf("pending_seconds = %v, want 42", ps)
	}
	if !cpu.Valid || cpu.Float64 != 4.0 {
		t.Errorf("cpu_request = %v, want 4.0", cpu)
	}
	if !mem.Valid || mem.Float64 != 16.0 {
		t.Errorf("mem_request_gb = %v, want 16.0", mem)
	}
	if !src.Valid || src.String != "template:nf-core/rnaseq" {
		t.Errorf("submission_source = %v, want template:nf-core/rnaseq", src)
	}

	if err := db.QueryRow(`SELECT pending_seconds, cpu_request, mem_request_gb, submission_source
	                         FROM runs WHERE run_id = ?`, "r-null").Scan(&ps, &cpu, &mem, &src); err != nil {
		t.Fatalf("read r-null: %v", err)
	}
	if ps.Valid || cpu.Valid || mem.Valid || src.Valid {
		t.Errorf("expected all NULL on r-null; got ps=%v cpu=%v mem=%v src=%v", ps, cpu, mem, src)
	}
}

// TestFeaturesIntroducedBy_NewMigrations: the 0008/0009/0010 entries appear
// in the feature table so the local-state pseudo-service advertises them
// through the capability layer.
func TestFeaturesIntroducedBy_NewMigrations(t *testing.T) {
	cases := map[string]string{
		"0008_runs_queue_wait":        "queue-wait-fraction",
		"0009_runs_resource_request":  "resource-fit",
		"0010_runs_submission_source": "template-vs-handwritten",
	}
	for version, want := range cases {
		got := FeaturesIntroducedBy(version)
		if len(got) != 1 || got[0] != want {
			t.Errorf("FeaturesIntroducedBy(%q) = %v, want [%s]", version, got, want)
		}
	}
}
