package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	_ "modernc.org/sqlite"
)

// TestSubmissionSourceClassifier_Resolve covers all four buckets the
// run-submit paths can land in, per spec abc-report.md §B.
func TestSubmissionSourceClassifier_Resolve(t *testing.T) {
	cases := []struct {
		name   string
		c      SubmissionSourceClassifier
		envSet bool
		want   string
	}{
		{"rerun wins over template", SubmissionSourceClassifier{TemplateID: "nf-core/rnaseq", Rerun: true}, false, "rerun"},
		{"rerun wins over automation", SubmissionSourceClassifier{Rerun: true}, true, "rerun"},
		{"automation env beats template", SubmissionSourceClassifier{TemplateID: "nf-core/rnaseq"}, true, "automation"},
		{"template when set", SubmissionSourceClassifier{TemplateID: "nf-core/rnaseq"}, false, "template:nf-core/rnaseq"},
		{"handwritten default", SubmissionSourceClassifier{}, false, "handwritten"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv("ABC_CLI_AUTOMATION", "1")
			} else {
				t.Setenv("ABC_CLI_AUTOMATION", "")
			}
			got := tc.c.Resolve()
			if got != tc.want {
				t.Errorf("Resolve() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAutoAttach_PersistsSubmissionSource: AutoAttachAndInsertRun writes
// the SubmissionSource value through to the runs row. Spec abc-report.md
// §B integration coverage: each of the four buckets is exercised.
func TestAutoAttach_PersistsSubmissionSource(t *testing.T) {
	cases := []string{"template:nf-core/rnaseq", "rerun", "automation", "handwritten"}
	for _, want := range cases {
		t.Run(want, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "local.db")
			dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
			db, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := migrations.Apply(db, path, "v0-test"); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			req := AutoAttachRequest{
				ContextName:      "test-ctx",
				NoProject:        true,
				NoInvestigation:  true,
				WorkloadRef:      "wf",
				Verb:             "pipeline",
				CPURequest:       2.0,
				MemRequestGB:     8.0,
				SubmissionSource: want,
			}
			res, err := AutoAttachAndInsertRun(context.Background(), db, nil, req)
			if err != nil {
				t.Fatalf("AutoAttach: %v", err)
			}

			var (
				gotSrc sql.NullString
				gotCPU sql.NullFloat64
				gotMem sql.NullFloat64
			)
			if err := db.QueryRow(
				`SELECT submission_source, cpu_request, mem_request_gb FROM runs WHERE run_id = ?`,
				res.RunID,
			).Scan(&gotSrc, &gotCPU, &gotMem); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if !gotSrc.Valid || gotSrc.String != want {
				t.Errorf("submission_source = %v, want %q", gotSrc, want)
			}
			if !gotCPU.Valid || gotCPU.Float64 != 2.0 {
				t.Errorf("cpu_request = %v, want 2.0", gotCPU)
			}
			if !gotMem.Valid || gotMem.Float64 != 8.0 {
				t.Errorf("mem_request_gb = %v, want 8.0", gotMem)
			}
		})
	}
}
