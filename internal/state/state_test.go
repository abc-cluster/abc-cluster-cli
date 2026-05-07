package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		// Drop the cache entry so subsequent tests get a fresh open.
		openMu.Lock()
		delete(cache, path)
		openMu.Unlock()
	})
	return db
}

func TestSchemaApplied(t *testing.T) {
	db := newTestDB(t)
	for _, table := range []string{"projects", "investigations", "annotations", "runs",
		"active_pointers", "cli_audit", "citations", "freezes",
		"container_digests", "pipeline_metadata", "telemetry_queue",
		"schema_migrations"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestProjectAndInvestigationFlow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	p := Project{
		ProjectID:   NewProjectID(),
		Slug:        "test-proj-1",
		ContextName: "default",
		Title:       "Test Project",
	}
	if _, err := CreateProject(ctx, db, p); err != nil {
		t.Fatal(err)
	}
	got, err := FindProject(ctx, db, "default", "test-proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != p.ProjectID {
		t.Errorf("FindProject returned wrong row")
	}

	pid := p.ProjectID
	if err := SetActivePointer(ctx, db, "default", PointerProject, &pid); err != nil {
		t.Fatal(err)
	}
	cur, err := GetActivePointer(ctx, db, "default", PointerProject)
	if err != nil {
		t.Fatal(err)
	}
	if cur != pid {
		t.Errorf("active pointer mismatch: %s vs %s", cur, pid)
	}

	inv := Investigation{
		InvestigationID: NewInvestigationID(),
		Slug:            "test-inv-1",
		ContextName:     "default",
		ProjectID:       sql.NullString{String: p.ProjectID, Valid: true},
		Title:           "Test inv",
	}
	if _, err := CreateInvestigation(ctx, db, inv); err != nil {
		t.Fatal(err)
	}

	a := Annotation{
		AnnotationID:    NewAnnotationID(),
		InvestigationID: inv.InvestigationID,
		Body:            "first thought",
	}
	if _, err := AddAnnotation(ctx, db, a); err != nil {
		t.Fatal(err)
	}

	cit := Citation{
		SourceAnnotationID:  a.AnnotationID,
		TargetInvestigation: inv.InvestigationID,
	}
	if err := AddCitation(ctx, db, cit); err != nil {
		t.Fatal(err)
	}
	cits, err := ListCitationsFromInvestigation(ctx, db, inv.InvestigationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cits) != 1 {
		t.Errorf("expected 1 citation, got %d", len(cits))
	}
}

func TestSlugUniqueness(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p1 := Project{ProjectID: NewProjectID(), Slug: "dup", ContextName: "default", Title: "p1"}
	if _, err := CreateProject(ctx, db, p1); err != nil {
		t.Fatal(err)
	}
	exists, err := SlugExistsProject(ctx, db, "default", "dup")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Errorf("expected slug 'dup' to exist")
	}
	exists, err = SlugExistsProject(ctx, db, "default", "other")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("expected slug 'other' not to exist")
	}
}

func TestIdempotentReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db1, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db1.Exec(`INSERT INTO projects (project_id, slug, context_name, title, status, created_at, updated_at) VALUES ('P-x', 's', 'c', 't', 'active', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	db1.Close()
	openMu.Lock()
	delete(cache, path)
	openMu.Unlock()

	db2, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var n int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM projects WHERE project_id='P-x'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("data lost on reopen: count=%d", n)
	}
}
