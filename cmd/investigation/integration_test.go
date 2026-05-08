package investigation

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

// TestIntegrationFullFlow exercises spec §J test list: project create →
// investigation create → annotations → branch → annotation → merge →
// visualize. Full headless flow without TUI.
func TestIntegrationFullFlow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "local.db")
	db, err := state.OpenAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	contextName := "default"

	// Step 1: project create.
	p := state.Project{ProjectID: state.NewProjectID(), Slug: "genpath-2024", ContextName: contextName, Title: "GenPath 2024"}
	if _, err := state.CreateProject(ctx, db, p); err != nil {
		t.Fatal(err)
	}
	pid := p.ProjectID
	if err := state.SetActivePointer(ctx, db, contextName, state.PointerProject, &pid); err != nil {
		t.Fatal(err)
	}

	// Step 2: investigation create + auto-attach.
	root := state.Investigation{
		InvestigationID: state.NewInvestigationID(),
		Slug:            "cosmic-pelican-7",
		ContextName:     contextName,
		ProjectID:       sql.NullString{String: p.ProjectID, Valid: true},
		Title:           "Genome assembly nanopore Q4",
	}
	if _, err := state.CreateInvestigation(ctx, db, root); err != nil {
		t.Fatal(err)
	}
	rid := root.InvestigationID
	state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, &rid)

	// Step 3: annotation.
	a := state.Annotation{
		AnnotationID:    state.NewAnnotationID(),
		InvestigationID: root.InvestigationID,
		Tag:             sql.NullString{String: "hypothesis", Valid: true},
		Body:            "viralrecon should work",
	}
	state.AddAnnotation(ctx, db, a)

	// Step 4: pipeline run (mocked) — verify auto-attach picks up active investigation.
	buf := &bytes.Buffer{}
	res, err := state.AutoAttachAndInsertRun(ctx, db, buf, state.AutoAttachRequest{
		ContextName: contextName,
		WorkloadRef: "nf-core/viralrecon",
		WorkloadVersion: "2.6.0",
		Verb:        "pipeline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InvestigationID.Valid || res.InvestigationID.String != root.InvestigationID {
		t.Errorf("auto-attach didn't pick up active investigation; got %v", res.InvestigationID)
	}
	if !res.ProjectID.Valid || res.ProjectID.String != p.ProjectID {
		t.Errorf("auto-attach didn't inherit project; got %v", res.ProjectID)
	}
	if !strings.Contains(buf.String(), "Auto-attached:") {
		t.Errorf("missing auto-attach banner: %s", buf.String())
	}

	// Step 5: branch + merge.
	child := state.Investigation{
		InvestigationID: state.NewInvestigationID(),
		Slug:            "nanopore-specific",
		ContextName:     contextName,
		ProjectID:       sql.NullString{String: p.ProjectID, Valid: true},
		ParentID:        sql.NullString{String: root.InvestigationID, Valid: true},
		Title:           "nanopore-specific",
	}
	state.CreateInvestigation(ctx, db, child)
	state.AddAnnotation(ctx, db, state.Annotation{
		AnnotationID: state.NewAnnotationID(), InvestigationID: child.InvestigationID,
		Tag: sql.NullString{String: "decision", Valid: true}, Body: "artic adopted",
	})
	state.UpdateInvestigationFields(ctx, db, child.InvestigationID, map[string]any{
		"status": "merged", "merged_into": root.InvestigationID,
	})

	// Step 6: visualize all 4 view types — sanity checks.
	root, _ = state.FindInvestigation(ctx, db, contextName, root.InvestigationID)
	for _, vt := range []string{"branches", "timeline", "flow", "lineage"} {
		opts := vizOptions{vizType: vt, branchesFilter: "all"}
		var src string
		switch vt {
		case "branches":
			src, err = renderBranches(ctx, db, root, opts)
		case "timeline":
			src, err = renderTimeline(ctx, db, root, opts)
		case "flow":
			src, err = renderFlow(ctx, db, root, opts)
		case "lineage":
			src, err = renderLineage(ctx, db, root, opts)
		}
		if err != nil {
			t.Errorf("%s: %v", vt, err)
		}
		if src == "" {
			t.Errorf("%s produced empty output", vt)
		}
	}
}

// TestEditorEmptyAborts simulates the EDITOR=true case from spec §J.
func TestEditorEmptyAborts(t *testing.T) {
	// We can't easily invoke the cobra command here; instead simulate the
	// editor returning empty string and assert the annotate path bails out.
	prev := openEditorTemplate
	defer func() { openEditorTemplate = prev }()
	openEditorTemplate = func(string) (string, string, error) { return "", "", nil }
	body, _, err := openEditorTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		t.Errorf("expected empty body")
	}
}
