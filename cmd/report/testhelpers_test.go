package report

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	_ "modernc.org/sqlite"
)

// openTestFixtureDB returns a fresh SQLite at a temp path with all
// embedded migrations applied. Used by the no-network test. Mirrors
// the helper internal/report uses but lives here so the verb tests
// don't reach across packages.
func openTestFixtureDB(t *testing.T) *sql.DB {
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
		t.Fatalf("migrations.Apply: %v", err)
	}
	return db
}
