package accounting

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	_ "modernc.org/sqlite"
)

// TestIntegration_AccountingReportEndToEnd seeds two completed runs into
// a temp ~/.abc/local.db, then runs the parent `abc accounting` command
// and asserts that the table + rate-card footer both appear in the
// output with the correct totals.
//
// This is the spec's "Integration test" §F item (full flow against a
// temp DB). Mocks completed runs by INSERTing rows directly with
// completed_at + cpu_hours set, per the spec's note for the case where
// the run-watcher write-back from abc-investigation §E hasn't landed.
func TestIntegration_AccountingReportEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("HOME", dir)
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	const seedCfg = `version: "1.0"
active_context: ctx
contexts:
  ctx:
    endpoint: https://example.test
`
	if err := os.MkdirAll(filepath.Join(dir, ".abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(seedCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// Open a fresh DB at the spot state.Open() will look (~/.abc/local.db).
	homeDB := filepath.Join(dir, ".abc", "local.db")
	dsn := "file:" + homeDB + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(db, homeDB, "v0.1.25-test"); err != nil {
		t.Fatalf("Apply migrations: %v", err)
	}
	now := time.Now().Unix()
	_, err = db.Exec(`PRAGMA foreign_keys = OFF`)
	if err != nil {
		t.Fatal(err)
	}
	for i, ns := range []string{"ns-a", "ns-b"} {
		_, err = db.Exec(`INSERT INTO runs
			(run_id, context_name, project_id, investigation_id, verb, workload_ref, namespace,
			 submitted_at, completed_at, status, cpu_hours, memory_gb_hours, walltime_seconds, gpu_count, created_at)
			VALUES (?, ?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
			"run-"+ns, "ctx", "pipeline", "nf-rnaseq", ns,
			now-1000-int64(i*100), now-500-int64(i*100), "completed", 4.0, 10.0, 500, now-1000)
		if err != nil {
			t.Fatalf("seed run: %v", err)
		}
	}
	// Force the cached state DB handle to point at our temp path. The state
	// package's Open() resolves via os.UserHomeDir() which honours $HOME on
	// Unix, so setting HOME above is sufficient.

	dbCloseHack(t)

	// Run the parent accounting command (no subcommand → local report).
	rootCmd := NewCmd()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--by=namespace"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Namespace", "Cost (ZAR)",
		"ns-a", "ns-b",
		"Rate card (effective):", "cost.cpu_hour",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Each ns: 4 cpu·h * 0.50 + 10 mem·h * 0.05 = 2.0 + 0.5 = 2.50 ZAR.
	if !strings.Contains(out, "2.50") {
		t.Errorf("expected per-namespace total 2.50, output:\n%s", out)
	}
}

// dbCloseHack drops the cached state.DB handle held by internal/state so a
// subsequent state.Open() picks up our temp $HOME-rooted path. Without
// this, an earlier Open() in the same test process pins the cache to its
// path.
func dbCloseHack(t *testing.T) {
	t.Helper()
	_ = state.Close()
	t.Cleanup(func() { _ = state.Close() })
}
