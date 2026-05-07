package state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
)

// AutoAttachRequest captures the inputs the auto-attach resolver uses.
type AutoAttachRequest struct {
	ContextName        string // cluster context name
	NoProject          bool
	NoInvestigation    bool
	ProjectFlag        string // --project=<slug-or-id>; "" if unset
	InvestigationFlag  string // --investigation=<slug-or-id>; "" if unset
	WorkloadRef        string // pipeline name / script path / module ref
	WorkloadVersion    string
	Verb               string // "pipeline" | "job" | "module"
	Namespace          string
	Workspace          string
	ParamsJSON         string
	GpuCount           int // 0 == NULL on insert
}

// AutoAttachResult is the resolved row that gets inserted into runs.
type AutoAttachResult struct {
	RunID            string
	ProjectID        sql.NullString
	InvestigationID  sql.NullString
	ProjectSlug      string
	InvestigationSlug string
}

// AutoAttachAndInsertRun resolves project/investigation per the precedence in
// spec §E and inserts a runs row. Always returns a usable row even if both
// pointers are null. Errors only on DB issues.
func AutoAttachAndInsertRun(ctx context.Context, db *sql.DB, banner io.Writer, req AutoAttachRequest) (AutoAttachResult, error) {
	r := AutoAttachResult{RunID: NewRunID()}

	var (
		projectID       sql.NullString
		investigationID sql.NullString
	)

	// ----- Investigation precedence -----
	if !req.NoInvestigation {
		ref := req.InvestigationFlag
		if ref == "" {
			ref = os.Getenv("ABC_INVESTIGATION")
		}
		if ref == "" {
			ref, _ = GetActivePointer(ctx, db, req.ContextName, PointerInvestigation)
		}
		if ref != "" {
			inv, err := FindInvestigation(ctx, db, req.ContextName, ref)
			if err == nil {
				investigationID = sql.NullString{String: inv.InvestigationID, Valid: true}
				r.InvestigationSlug = inv.Slug
				if inv.ProjectID.Valid {
					// Investigation's project takes precedence.
					projectID = inv.ProjectID
				}
			}
		}
	}

	// ----- Project precedence (only if not already inherited from investigation) -----
	if !req.NoProject && !projectID.Valid {
		ref := req.ProjectFlag
		if ref == "" {
			ref = os.Getenv("ABC_PROJECT")
		}
		if ref == "" {
			ref, _ = GetActivePointer(ctx, db, req.ContextName, PointerProject)
		}
		if ref != "" {
			p, err := FindProject(ctx, db, req.ContextName, ref)
			if err == nil {
				projectID = sql.NullString{String: p.ProjectID, Valid: true}
				r.ProjectSlug = p.Slug
			}
		}
	} else if projectID.Valid && r.ProjectSlug == "" {
		// Lookup the inherited project slug for the banner.
		p, err := FindProject(ctx, db, req.ContextName, projectID.String)
		if err == nil {
			r.ProjectSlug = p.Slug
		}
	}

	r.ProjectID = projectID
	r.InvestigationID = investigationID

	run := Run{
		RunID:           r.RunID,
		ContextName:     req.ContextName,
		ProjectID:       projectID,
		InvestigationID: investigationID,
		Verb:            req.Verb,
		WorkloadRef:     req.WorkloadRef,
		WorkloadVersion: sql.NullString{String: req.WorkloadVersion, Valid: req.WorkloadVersion != ""},
		ParamsJSON:      sql.NullString{String: req.ParamsJSON, Valid: req.ParamsJSON != ""},
		Namespace:       sql.NullString{String: req.Namespace, Valid: req.Namespace != ""},
		Workspace:       sql.NullString{String: req.Workspace, Valid: req.Workspace != ""},
		GpuCount:        sql.NullInt64{Int64: int64(req.GpuCount), Valid: req.GpuCount > 0},
		Status:          "running",
	}
	if err := InsertRun(ctx, db, run); err != nil {
		return r, err
	}

	// Banner
	if banner != nil {
		ps := r.ProjectSlug
		if ps == "" {
			ps = "(none)"
		}
		is := r.InvestigationSlug
		if is == "" {
			is = "(none)"
		}
		fmt.Fprintf(banner, "[abc] Auto-attached: project %s, investigation %s (run %s)\n", ps, is, r.RunID)
	}
	return r, nil
}
