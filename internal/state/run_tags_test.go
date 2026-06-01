package state

import (
	"context"
	"database/sql"
	"testing"
)

// TestRunTagsRoundTripAndAttribution covers spec abc-job-data-staging-and-run-tags
// B1 (runs.tags_json) + B2 (run tagging + investigation attribution) + B4 (many
// runs across distinct notebook= tags attributed to one investigation).
func TestRunTagsRoundTripAndAttribution(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	inv := Investigation{
		InvestigationID: NewInvestigationID(),
		Slug:            "species-classification",
		ContextName:     "default",
		Title:           "Penguin species classification",
	}
	if _, err := CreateInvestigation(ctx, db, inv); err != nil {
		t.Fatal(err)
	}

	// Three runs from three notebooks, same investigation, distinct model tag —
	// the MLflow-style "tag runs, compare by tag" model.
	cases := []struct{ notebook, model string }{
		{"knn", "knn"},
		{"random-forest", "rf"},
		{"svm", "svm"},
	}
	for _, c := range cases {
		req := AutoAttachRequest{
			ContextName:       "default",
			InvestigationFlag: inv.Slug, // attribute via the active-investigation path
			WorkloadRef:       c.notebook + ".py",
			Verb:              "job",
			Tags:              []string{"model=" + c.model, "notebook=" + c.notebook},
		}
		if _, err := AutoAttachAndInsertRun(ctx, db, nil, req); err != nil {
			t.Fatalf("AutoAttachAndInsertRun(%s): %v", c.notebook, err)
		}
	}

	runs, err := ListRunsForInvestigation(ctx, db, inv.InvestigationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs attributed to the investigation, got %d", len(runs))
	}

	// Every run carries its tags (round-trip through tags_json) and is attributed.
	seenModels := map[string]bool{}
	seenNotebooks := map[string]bool{}
	for _, r := range runs {
		if !r.InvestigationID.Valid || r.InvestigationID.String != inv.InvestigationID {
			t.Errorf("run %s not attributed to investigation", r.RunID)
		}
		if len(r.Tags) != 2 {
			t.Errorf("run %s: expected 2 tags, got %v", r.RunID, r.Tags)
		}
		for _, tg := range r.Tags {
			switch {
			case len(tg) > 6 && tg[:6] == "model=":
				seenModels[tg[6:]] = true
			case len(tg) > 9 && tg[:9] == "notebook=":
				seenNotebooks[tg[9:]] = true
			}
		}
	}
	for _, m := range []string{"knn", "rf", "svm"} {
		if !seenModels[m] {
			t.Errorf("model=%s tag missing after round-trip", m)
		}
	}
	if len(seenNotebooks) != 3 {
		t.Errorf("expected 3 distinct notebook= tags, got %d (%v)", len(seenNotebooks), seenNotebooks)
	}
}

// TestRunTagsNullWhenEmpty: a run with no tags stores NULL/empty and reads back nil.
func TestRunTagsNullWhenEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	inv := Investigation{InvestigationID: NewInvestigationID(), Slug: "no-tags", ContextName: "default", Title: "x"}
	if _, err := CreateInvestigation(ctx, db, inv); err != nil {
		t.Fatal(err)
	}
	r := Run{RunID: NewRunID(), ContextName: "default", InvestigationID: sql.NullString{String: inv.InvestigationID, Valid: true}, Verb: "job", WorkloadRef: "x.py"}
	if err := InsertRun(ctx, db, r); err != nil {
		t.Fatal(err)
	}
	got, err := ListRunsForInvestigation(ctx, db, inv.InvestigationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Tags) != 0 {
		t.Fatalf("expected 1 run with no tags, got %d runs / tags %v", len(got), got)
	}
}
