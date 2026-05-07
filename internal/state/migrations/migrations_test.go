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
	path := filepath.Join(dir, "state.db")
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
	path := filepath.Join(dir, "state.db")
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
	path := filepath.Join(dir, "state.db")
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
	path := filepath.Join(dir, "state.db")
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
	path := filepath.Join(dir, "state.db")
	// Touch the DB so backup() has a source to copy.
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create maxBackups + 2 fake backups with staggered mtimes.
	for i := 0; i < maxBackups+2; i++ {
		name := filepath.Join(dir, "state.db.backup-pre-0001_initial-"+string(rune('a'+i)))
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
