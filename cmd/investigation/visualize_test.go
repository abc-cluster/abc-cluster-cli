package investigation

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

// setupTestDB creates a fresh DB and returns it. Caller is responsible for
// not relying on the package-level cache.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, err := state.OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestVisualizeBranches exercises Example 1 from the brainstorm: viralrecon fail
// on main, branch nanopore-specific, two runs + annotations, merge, final run.
func TestVisualizeBranches(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	p := state.Project{ProjectID: state.NewProjectID(), Slug: "genpath-2024",
		ContextName: "default", Title: "GenPath 2024 Mozambique"}
	if _, err := state.CreateProject(ctx, db, p); err != nil {
		t.Fatal(err)
	}
	root := state.Investigation{
		InvestigationID: state.NewInvestigationID(),
		Slug:            "cosmic-pelican-7",
		ContextName:     "default",
		ProjectID:       sql.NullString{String: p.ProjectID, Valid: true},
		Title:           "Genome assembly nanopore Q4",
	}
	if _, err := state.CreateInvestigation(ctx, db, root); err != nil {
		t.Fatal(err)
	}
	mustAnnotate(t, ctx, db, root.InvestigationID, "hypothesis", "viralrecon should work")
	mustRun(t, ctx, db, root.InvestigationID, "nf-core/viralrecon", "2.6.0", "failed")
	mustAnnotate(t, ctx, db, root.InvestigationID, "issue", "doesn't handle long reads")

	// Branch
	child := state.Investigation{
		InvestigationID: state.NewInvestigationID(),
		Slug:            "nanopore-specific",
		ContextName:     "default",
		ProjectID:       sql.NullString{String: p.ProjectID, Valid: true},
		ParentID:        sql.NullString{String: root.InvestigationID, Valid: true},
		Title:           "nanopore-specific approach",
	}
	if _, err := state.CreateInvestigation(ctx, db, child); err != nil {
		t.Fatal(err)
	}
	mustRun(t, ctx, db, child.InvestigationID, "artic-network/fieldbioinformatics", "1.4", "completed")
	mustAnnotate(t, ctx, db, child.InvestigationID, "decision", "going with artic")

	// Merge
	if err := state.UpdateInvestigationFields(ctx, db, child.InvestigationID, map[string]any{
		"status": "merged", "merged_into": root.InvestigationID,
	}); err != nil {
		t.Fatal(err)
	}

	src, err := renderBranches(ctx, db, root, vizOptions{vizType: "branches", branchesFilter: "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gitGraph", "branch nanopore-specific", "merge nanopore-specific", "REVERSE", "HIGHLIGHT"} {
		if !strings.Contains(src, want) {
			t.Errorf("branches output missing %q\n---\n%s", want, src)
		}
	}
}

func TestVisualizeTimeline(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	p := state.Project{ProjectID: state.NewProjectID(), Slug: "p1", ContextName: "default", Title: "p"}
	state.CreateProject(ctx, db, p)
	inv := state.Investigation{InvestigationID: state.NewInvestigationID(), Slug: "tl1", ContextName: "default",
		ProjectID: sql.NullString{String: p.ProjectID, Valid: true}, Title: "tl"}
	state.CreateInvestigation(ctx, db, inv)
	mustAnnotate(t, ctx, db, inv.InvestigationID, "hypothesis", "h1")
	mustRun(t, ctx, db, inv.InvestigationID, "nf/foo", "1.0", "completed")
	src, err := renderTimeline(ctx, db, inv, vizOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "timeline") {
		t.Errorf("missing timeline directive: %s", src)
	}
}

func TestVisualizeFlow(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	p := state.Project{ProjectID: state.NewProjectID(), Slug: "p1", ContextName: "default", Title: "p"}
	state.CreateProject(ctx, db, p)
	inv := state.Investigation{InvestigationID: state.NewInvestigationID(), Slug: "fl1", ContextName: "default",
		ProjectID: sql.NullString{String: p.ProjectID, Valid: true}, Title: "fl"}
	state.CreateInvestigation(ctx, db, inv)
	mustAnnotate(t, ctx, db, inv.InvestigationID, "hypothesis", "h1")
	mustRun(t, ctx, db, inv.InvestigationID, "nf/foo", "1.0", "failed")
	mustAnnotate(t, ctx, db, inv.InvestigationID, "issue", "i1")
	src, err := renderFlow(ctx, db, inv, vizOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "flowchart TD") {
		t.Errorf("missing flowchart TD: %s", src)
	}
	if !strings.Contains(src, "|failed|") {
		t.Errorf("expected failed edge label: %s", src)
	}
}

func TestVisualizeLineageWithCitation(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	p := state.Project{ProjectID: state.NewProjectID(), Slug: "p1", ContextName: "default", Title: "p"}
	state.CreateProject(ctx, db, p)
	cosmic := state.Investigation{InvestigationID: state.NewInvestigationID(), Slug: "cosmic-pelican-7",
		ContextName: "default", ProjectID: sql.NullString{String: p.ProjectID, Valid: true}, Title: "cosmic"}
	state.CreateInvestigation(ctx, db, cosmic)
	insightID := mustAnnotate(t, ctx, db, cosmic.InvestigationID, "insight", "primer scheme matters")

	quiet := state.Investigation{InvestigationID: state.NewInvestigationID(), Slug: "quiet-falcon-9",
		ContextName: "default", ProjectID: sql.NullString{String: p.ProjectID, Valid: true}, Title: "rna"}
	state.CreateInvestigation(ctx, db, quiet)
	srcAnnID := mustAnnotate(t, ctx, db, quiet.InvestigationID, "observation", "may apply here")

	if err := state.AddCitation(ctx, db, state.Citation{
		SourceAnnotationID:  srcAnnID,
		TargetInvestigation: cosmic.InvestigationID,
		TargetAnnotationID:  sql.NullString{String: insightID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	src, err := renderLineage(ctx, db, cosmic, vizOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "flowchart LR") {
		t.Errorf("missing flowchart LR: %s", src)
	}
	if !strings.Contains(src, "-.->") {
		t.Errorf("missing dotted citation arrow: %s", src)
	}
}

func mustAnnotate(t *testing.T, ctx context.Context, db *sql.DB, invID, tag, body string) string {
	t.Helper()
	a := state.Annotation{
		AnnotationID:    state.NewAnnotationID(),
		InvestigationID: invID,
		Tag:             sql.NullString{String: tag, Valid: tag != ""},
		Body:            body,
	}
	if _, err := state.AddAnnotation(ctx, db, a); err != nil {
		t.Fatal(err)
	}
	return a.AnnotationID
}

func mustRun(t *testing.T, ctx context.Context, db *sql.DB, invID, ref, version, status string) string {
	t.Helper()
	r := state.Run{
		RunID:           state.NewRunID(),
		ContextName:     "default",
		InvestigationID: sql.NullString{String: invID, Valid: true},
		Verb:            "pipeline",
		WorkloadRef:     ref,
		WorkloadVersion: sql.NullString{String: version, Valid: version != ""},
		Status:          status,
	}
	if err := state.InsertRun(ctx, db, r); err != nil {
		t.Fatal(err)
	}
	return r.RunID
}
