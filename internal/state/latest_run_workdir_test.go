package state

import (
	"context"
	"database/sql"
	"testing"
)

func TestLatestRunForWorkdirRoot(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	const wd = "s3://bucket/scope/user/workdir/user-1000/"
	const other = "s3://bucket/scope/user/workdir/user-2000/"

	mk := func(id, version, root string, submittedAt int64) {
		r := Run{
			RunID:           id,
			ContextName:     "default",
			Verb:            "pipeline",
			WorkloadRef:     "https://github.com/org/pipe",
			WorkloadVersion: sql.NullString{String: version, Valid: version != ""},
			SubmittedAt:     submittedAt,
		}
		if err := InsertRun(ctx, db, r); err != nil {
			t.Fatalf("InsertRun(%s): %v", id, err)
		}
		if err := UpdateRunWorkdirRoot(ctx, db, id, root); err != nil {
			t.Fatalf("UpdateRunWorkdirRoot(%s): %v", id, err)
		}
	}

	// Two runs share the work-dir; the later submit must win. A third run on a
	// different work-dir must not leak in.
	mk("run-old", "5fac726e3c57ea5737c07ed7690f965d19ee48b9", wd, 100)
	mk("run-new", "8988b99506c7702e65ca1a64a52bc48b069955a2", wd, 200)
	mk("run-other", "deadbeef", other, 300)

	got, ok, err := LatestRunForWorkdirRoot(ctx, db, wd)
	if err != nil {
		t.Fatalf("LatestRunForWorkdirRoot: %v", err)
	}
	if !ok {
		t.Fatal("expected a run for the work-dir, got none")
	}
	if got.RunID != "run-new" {
		t.Errorf("expected latest run-new, got %s", got.RunID)
	}
	if got.WorkloadVersion.String != "8988b99506c7702e65ca1a64a52bc48b069955a2" {
		t.Errorf("expected the newer run's revision, got %q", got.WorkloadVersion.String)
	}

	// Unknown work-dir → ok=false, no error.
	_, ok, err = LatestRunForWorkdirRoot(ctx, db, "s3://bucket/scope/user/workdir/never/")
	if err != nil {
		t.Fatalf("unexpected error for unknown work-dir: %v", err)
	}
	if ok {
		t.Error("expected ok=false for unknown work-dir")
	}
}
