package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Project mirrors the projects row.
type Project struct {
	ProjectID   string
	Slug        string
	ContextName string
	Title       string
	Description string
	Status      string
	Tags        []string
	CreatedAt   int64
	UpdatedAt   int64
}

// Investigation mirrors the investigations row.
type Investigation struct {
	InvestigationID string
	Slug            string
	ContextName     string
	ProjectID       sql.NullString
	ParentID        sql.NullString
	Title           string
	Status          string
	MergedInto      sql.NullString
	DeadEndReason   sql.NullString
	Tags            []string
	CreatedAt       int64
	UpdatedAt       int64
}

// Annotation mirrors the annotations row.
//
// Tag is the legacy singular column (kept for backward compat; populated on
// reads of pre-0007 rows). Tags is the canonical multi-tag list, persisted
// as `tags_json`. New writes populate Tags; the legacy `tag` column is
// written as the first element if present, for cross-version readers.
type Annotation struct {
	AnnotationID     string
	InvestigationID  string
	Tag              sql.NullString // legacy singular tag (read-only on write path)
	Tags             []string       // tags_json — canonical
	Body             string
	CarriedFrom      sql.NullString
	CreatedAt        int64
	UpdatedAt        int64
	WithdrawnAt      sql.NullInt64
	WithdrawnReason  sql.NullString
}

// Run mirrors the runs row (subset most callers need).
type Run struct {
	RunID            string
	ContextName      string
	ProjectID        sql.NullString
	InvestigationID  sql.NullString
	Verb             string
	WorkloadRef      string
	WorkloadVersion  sql.NullString
	ParamsJSON       sql.NullString
	Namespace        sql.NullString
	Workspace        sql.NullString
	SubmittedAt      int64
	CompletedAt      sql.NullInt64
	Status           string
	ExitReason       sql.NullString
	CPUHours         sql.NullFloat64
	MemoryGBHours    sql.NullFloat64
	WalltimeSeconds  sql.NullInt64
	GpuCount         sql.NullInt64
	// ScratchGB is the per-run scratch reservation (GB). Combined with
	// walltime_seconds at report time to compute scratch_gb_hours, mirroring
	// how gpu_hours is derived from gpu_count.
	ScratchGB        sql.NullFloat64
	FreezeID         sql.NullString
	// PendingSeconds is the time the alloc spent in pending before
	// transitioning to running. Set by the run-watcher; NULL on rows
	// predating migration 0008.
	PendingSeconds   sql.NullInt64
	// CPURequest / MemRequestGB capture what the user asked for at submit
	// time (in cores and GB respectively), regardless of what the alloc
	// actually consumed. NULL on rows predating migration 0009.
	CPURequest       sql.NullFloat64
	MemRequestGB     sql.NullFloat64
	// SubmissionSource records how the run was authored:
	// "template:<id>" / "handwritten" / "rerun" / "automation".
	// NULL on rows predating migration 0010.
	SubmissionSource sql.NullString
	// NomadJobID is the Nomad job ID for this run, stored synchronously by
	// runner.Watch() before the background poll goroutine starts.
	// NULL for historical rows and --dry-run/--no-submit submissions.
	// Used by runner.ReconcileStuckRuns() to re-probe Nomad for runs that
	// were not completed by the background goroutine (e.g. CLI exited early).
	// NULL on rows predating migration 0011.
	NomadJobID sql.NullString
	// Tags are MLflow-style key=value run tags (e.g. "model=rf",
	// "notebook=rf.ipynb"), serialized as the tags_json JSON array.
	// Enable compare-by-tag within an investigation. NULL on rows
	// predating migration 0015.
	Tags []string
}

// Citation mirrors the citations row.
type Citation struct {
	CitationID         int64
	SourceAnnotationID string
	TargetInvestigation string
	TargetAnnotationID sql.NullString
	CreatedAt          int64
}

func tagsToJSON(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func jsonToTags(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// ----- Projects -----

// CreateProject inserts a new project row and returns it. Caller supplies
// project_id, slug; this fn fills in timestamps.
func CreateProject(ctx context.Context, db *sql.DB, p Project) (Project, error) {
	now := time.Now().Unix()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = "active"
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		// If we already started a tx, retry as: just go via Tx — modernc supports
		// IMMEDIATE through its own DSN, but we use Begin transactions here.
		// Continue without IMMEDIATE; SQLite will upgrade on first write.
		_ = err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO projects (project_id, slug, context_name, title, description, status, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectID, p.Slug, p.ContextName, p.Title, p.Description, p.Status, tagsToJSON(p.Tags), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// SlugExistsProject reports whether (context, slug) is taken.
func SlugExistsProject(ctx context.Context, db *sql.DB, contextName, slug string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE context_name = ? AND slug = ?`,
		contextName, slug).Scan(&n)
	return n > 0, err
}

// FindProject resolves slug-or-ID-prefix to a project within a context.
// Returns ErrNotFound if no match. Returns an error on ambiguous prefix.
func FindProject(ctx context.Context, db *sql.DB, contextName, ref string) (Project, error) {
	// Try slug first.
	p, err := scanProjectRow(db.QueryRowContext(ctx,
		`SELECT project_id, slug, context_name, title, COALESCE(description,''), status, COALESCE(tags_json,''), created_at, updated_at
		 FROM projects WHERE context_name = ? AND slug = ?`, contextName, ref))
	if err == nil {
		return p, nil
	}
	if err != sql.ErrNoRows {
		return p, err
	}
	// ID or ID prefix.
	rows, err := db.QueryContext(ctx,
		`SELECT project_id, slug, context_name, title, COALESCE(description,''), status, COALESCE(tags_json,''), created_at, updated_at
		 FROM projects WHERE context_name = ? AND project_id LIKE ?`, contextName, ref+"%")
	if err != nil {
		return p, err
	}
	defer rows.Close()
	matches := []Project{}
	for rows.Next() {
		x, err := scanProjectRows(rows)
		if err != nil {
			return p, err
		}
		matches = append(matches, x)
	}
	if len(matches) == 0 {
		return p, ErrNotFound
	}
	if len(matches) > 1 {
		return p, fmt.Errorf("ambiguous project ref %q (%d matches)", ref, len(matches))
	}
	return matches[0], nil
}

func scanProjectRow(r *sql.Row) (Project, error) {
	var p Project
	var tags string
	err := r.Scan(&p.ProjectID, &p.Slug, &p.ContextName, &p.Title, &p.Description, &p.Status, &tags, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Tags = jsonToTags(tags)
	return p, nil
}

func scanProjectRows(r *sql.Rows) (Project, error) {
	var p Project
	var tags string
	err := r.Scan(&p.ProjectID, &p.Slug, &p.ContextName, &p.Title, &p.Description, &p.Status, &tags, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Tags = jsonToTags(tags)
	return p, nil
}

// ListProjects returns projects for a context. status filters; "" means all.
func ListProjects(ctx context.Context, db *sql.DB, contextName, status string, allContexts bool) ([]Project, error) {
	q := `SELECT project_id, slug, context_name, title, COALESCE(description,''), status, COALESCE(tags_json,''), created_at, updated_at FROM projects`
	args := []any{}
	conds := []string{}
	if !allContexts {
		conds = append(conds, "context_name = ?")
		args = append(args, contextName)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY updated_at DESC"
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProjectRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProjectFields applies a sparse update.
func UpdateProjectFields(ctx context.Context, db *sql.DB, projectID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	parts := []string{}
	args := []any{}
	for k, v := range fields {
		parts = append(parts, k+" = ?")
		args = append(args, v)
	}
	parts = append(parts, "updated_at = ?")
	args = append(args, time.Now().Unix())
	args = append(args, projectID)
	_, err := db.ExecContext(ctx,
		"UPDATE projects SET "+strings.Join(parts, ", ")+" WHERE project_id = ?", args...)
	return err
}

// DeleteProject cascades to investigations and annotations.
func DeleteProject(ctx context.Context, db *sql.DB, projectID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Delete annotations first (FK), then investigations, then project.
	if _, err := tx.ExecContext(ctx, `DELETE FROM annotations WHERE investigation_id IN (SELECT investigation_id FROM investigations WHERE project_id = ?)`, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM investigations WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

// ----- Investigations -----

// CreateInvestigation inserts a new investigations row.
func CreateInvestigation(ctx context.Context, db *sql.DB, inv Investigation) (Investigation, error) {
	now := time.Now().Unix()
	inv.CreatedAt = now
	inv.UpdatedAt = now
	if inv.Status == "" {
		inv.Status = "active"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO investigations (investigation_id, slug, context_name, project_id, parent_id, title, status, merged_into, dead_end_reason, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.InvestigationID, inv.Slug, inv.ContextName, nullableString(inv.ProjectID), nullableString(inv.ParentID),
		inv.Title, inv.Status, nullableString(inv.MergedInto), nullableString(inv.DeadEndReason),
		tagsToJSON(inv.Tags), inv.CreatedAt, inv.UpdatedAt)
	return inv, err
}

func nullableString(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// SlugExistsInvestigation reports whether (context, slug) is taken in investigations.
func SlugExistsInvestigation(ctx context.Context, db *sql.DB, contextName, slug string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM investigations WHERE context_name = ? AND slug = ?`,
		contextName, slug).Scan(&n)
	return n > 0, err
}

// FindInvestigation resolves slug-or-ID(prefix).
func FindInvestigation(ctx context.Context, db *sql.DB, contextName, ref string) (Investigation, error) {
	// slug first
	inv, err := scanInvestigationRow(db.QueryRowContext(ctx, investigationSelect+
		` WHERE context_name = ? AND slug = ?`, contextName, ref))
	if err == nil {
		return inv, nil
	}
	if err != sql.ErrNoRows {
		return inv, err
	}
	rows, err := db.QueryContext(ctx, investigationSelect+
		` WHERE context_name = ? AND investigation_id LIKE ?`, contextName, ref+"%")
	if err != nil {
		return inv, err
	}
	defer rows.Close()
	matches := []Investigation{}
	for rows.Next() {
		x, err := scanInvestigationRows(rows)
		if err != nil {
			return inv, err
		}
		matches = append(matches, x)
	}
	if len(matches) == 0 {
		return inv, ErrNotFound
	}
	if len(matches) > 1 {
		return inv, fmt.Errorf("ambiguous investigation ref %q (%d matches)", ref, len(matches))
	}
	return matches[0], nil
}

const investigationSelect = `SELECT investigation_id, slug, context_name, project_id, parent_id, title, status, merged_into, dead_end_reason, COALESCE(tags_json,''), created_at, updated_at FROM investigations`

func scanInvestigationRow(r *sql.Row) (Investigation, error) {
	var i Investigation
	var tags string
	err := r.Scan(&i.InvestigationID, &i.Slug, &i.ContextName, &i.ProjectID, &i.ParentID, &i.Title, &i.Status, &i.MergedInto, &i.DeadEndReason, &tags, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return i, err
	}
	i.Tags = jsonToTags(tags)
	return i, nil
}

func scanInvestigationRows(r *sql.Rows) (Investigation, error) {
	var i Investigation
	var tags string
	err := r.Scan(&i.InvestigationID, &i.Slug, &i.ContextName, &i.ProjectID, &i.ParentID, &i.Title, &i.Status, &i.MergedInto, &i.DeadEndReason, &tags, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return i, err
	}
	i.Tags = jsonToTags(tags)
	return i, nil
}

// ListInvestigations returns investigations matching the filters.
func ListInvestigations(ctx context.Context, db *sql.DB, contextName string, projectID string, status string, allProjects bool) ([]Investigation, error) {
	q := investigationSelect
	args := []any{}
	conds := []string{"context_name = ?"}
	args = append(args, contextName)
	if !allProjects && projectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, projectID)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	q += " WHERE " + strings.Join(conds, " AND ") + " ORDER BY updated_at DESC"
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Investigation{}
	for rows.Next() {
		x, err := scanInvestigationRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// UpdateInvestigationFields applies a sparse update.
func UpdateInvestigationFields(ctx context.Context, db *sql.DB, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	parts := []string{}
	args := []any{}
	for k, v := range fields {
		parts = append(parts, k+" = ?")
		args = append(args, v)
	}
	parts = append(parts, "updated_at = ?")
	args = append(args, time.Now().Unix())
	args = append(args, id)
	_, err := db.ExecContext(ctx,
		"UPDATE investigations SET "+strings.Join(parts, ", ")+" WHERE investigation_id = ?", args...)
	return err
}

// ChildInvestigations returns immediate children of an investigation.
func ChildInvestigations(ctx context.Context, db *sql.DB, parentID string) ([]Investigation, error) {
	rows, err := db.QueryContext(ctx, investigationSelect+` WHERE parent_id = ? ORDER BY created_at`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Investigation{}
	for rows.Next() {
		x, err := scanInvestigationRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ----- Active pointers -----

const (
	PointerProject       = "project"
	PointerInvestigation = "investigation"
)

// SetActivePointer upserts the active pointer for (context, kind).
func SetActivePointer(ctx context.Context, db *sql.DB, contextName, kind string, value *string) error {
	now := time.Now().Unix()
	var v any
	if value == nil {
		v = nil
	} else {
		v = *value
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO active_pointers (context_name, pointer_kind, pointer_value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(context_name, pointer_kind) DO UPDATE
		SET pointer_value = excluded.pointer_value, updated_at = excluded.updated_at`,
		contextName, kind, v, now)
	return err
}

// GetActivePointer returns the pointer value (or "" if unset).
func GetActivePointer(ctx context.Context, db *sql.DB, contextName, kind string) (string, error) {
	var v sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT pointer_value FROM active_pointers WHERE context_name = ? AND pointer_kind = ?`,
		contextName, kind).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !v.Valid {
		return "", nil
	}
	return v.String, nil
}

// ----- Annotations -----

// AddAnnotation inserts an annotation row. Writes both legacy `tag` (first
// element of Tags if present, else a.Tag.String) and canonical `tags_json`.
func AddAnnotation(ctx context.Context, db *sql.DB, a Annotation) (Annotation, error) {
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	if a.UpdatedAt == 0 {
		a.UpdatedAt = a.CreatedAt
	}
	// Reconcile legacy/canonical tag fields. Prefer Tags slice; fall back to
	// the singular Tag column if the caller only set that.
	if len(a.Tags) == 0 && a.Tag.Valid && a.Tag.String != "" {
		a.Tags = []string{a.Tag.String}
	}
	legacyTag := sql.NullString{}
	if len(a.Tags) > 0 {
		legacyTag = sql.NullString{String: a.Tags[0], Valid: true}
	} else if a.Tag.Valid {
		legacyTag = a.Tag
	}
	tagsJSON := sql.NullString{}
	if len(a.Tags) > 0 {
		b, _ := json.Marshal(a.Tags)
		tagsJSON = sql.NullString{String: string(b), Valid: true}
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO annotations (annotation_id, investigation_id, tag, tags_json, body, carried_from, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AnnotationID, a.InvestigationID, nullableString(legacyTag), nullableString(tagsJSON),
		a.Body, nullableString(a.CarriedFrom), a.CreatedAt, a.UpdatedAt)
	return a, err
}

// annotationSelectCols is the canonical select list for reading annotations.
const annotationSelectCols = `annotation_id, investigation_id, tag, tags_json, body, carried_from,
	created_at, COALESCE(updated_at, created_at), withdrawn_at, withdrawn_reason`

// scanAnnotation scans a single row produced by annotationSelectCols.
func scanAnnotation(rows *sql.Rows, a *Annotation) error {
	var tagsJSON sql.NullString
	if err := rows.Scan(&a.AnnotationID, &a.InvestigationID, &a.Tag, &tagsJSON,
		&a.Body, &a.CarriedFrom, &a.CreatedAt, &a.UpdatedAt,
		&a.WithdrawnAt, &a.WithdrawnReason); err != nil {
		return err
	}
	if tagsJSON.Valid && tagsJSON.String != "" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &a.Tags)
	} else if a.Tag.Valid && a.Tag.String != "" {
		a.Tags = []string{a.Tag.String}
	}
	return nil
}

// ListAnnotations returns all annotations on an investigation in chrono order.
// By default, withdrawn annotations are excluded unless includeWithdrawn=true.
func ListAnnotations(ctx context.Context, db *sql.DB, investigationID string) ([]Annotation, error) {
	return ListAnnotationsFiltered(ctx, db, investigationID, false)
}

// ListAnnotationsFiltered returns annotations on an investigation. If
// includeWithdrawn is false, soft-deleted rows are excluded.
func ListAnnotationsFiltered(ctx context.Context, db *sql.DB, investigationID string, includeWithdrawn bool) ([]Annotation, error) {
	q := `SELECT ` + annotationSelectCols + ` FROM annotations WHERE investigation_id = ?`
	if !includeWithdrawn {
		q += ` AND withdrawn_at IS NULL`
	}
	q += ` ORDER BY created_at`
	rows, err := db.QueryContext(ctx, q, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := scanAnnotation(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FindAnnotation looks up an annotation by ID prefix scoped to an investigation.
func FindAnnotation(ctx context.Context, db *sql.DB, investigationID, ref string) (Annotation, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+annotationSelectCols+`
		FROM annotations WHERE investigation_id = ? AND annotation_id LIKE ?`,
		investigationID, ref+"%")
	if err != nil {
		return Annotation{}, err
	}
	defer rows.Close()
	matches := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := scanAnnotation(rows, &a); err != nil {
			return Annotation{}, err
		}
		matches = append(matches, a)
	}
	if len(matches) == 0 {
		return Annotation{}, ErrNotFound
	}
	if len(matches) > 1 {
		return Annotation{}, fmt.Errorf("ambiguous annotation ref %q", ref)
	}
	return matches[0], nil
}

// FindAnnotationGlobal looks up an annotation by ID prefix across all
// investigations. Used by `abc project investigation annotation <verb>` where
// the user supplies just the annotation prefix.
func FindAnnotationGlobal(ctx context.Context, db *sql.DB, ref string) (Annotation, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+annotationSelectCols+`
		FROM annotations WHERE annotation_id LIKE ?`, ref+"%")
	if err != nil {
		return Annotation{}, err
	}
	defer rows.Close()
	matches := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := scanAnnotation(rows, &a); err != nil {
			return Annotation{}, err
		}
		matches = append(matches, a)
	}
	if len(matches) == 0 {
		return Annotation{}, ErrNotFound
	}
	if len(matches) > 1 {
		return Annotation{}, fmt.Errorf("ambiguous annotation ref %q", ref)
	}
	return matches[0], nil
}

// AnnotationRevision mirrors annotation_revisions.
type AnnotationRevision struct {
	RevisionID            int64
	AnnotationID          string
	PrevBody              sql.NullString
	PrevTagsJSON          sql.NullString
	PrevInvestigationID   sql.NullString
	EditedAt              int64
	EditKind              string // 'body' | 'tag' | 'move' | 'withdraw' | 'restore'
	EditReason            sql.NullString
}

// AddAnnotationRevision records one previous-state row for an annotation
// mutation. Caller is responsible for performing this within the same
// transaction as the underlying annotations UPDATE for atomicity. For
// simplicity in this codebase we do best-effort separate writes; SQLite
// IMMEDIATE serialises writers regardless.
func AddAnnotationRevision(ctx context.Context, db *sql.DB, r AnnotationRevision) error {
	if r.EditedAt == 0 {
		r.EditedAt = time.Now().Unix()
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO annotation_revisions
		  (annotation_id, prev_body, prev_tags_json, prev_investigation_id, edited_at, edit_kind, edit_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.AnnotationID, nullableString(r.PrevBody), nullableString(r.PrevTagsJSON),
		nullableString(r.PrevInvestigationID), r.EditedAt, r.EditKind, nullableString(r.EditReason))
	return err
}

// ListAnnotationRevisions returns all revisions for an annotation, oldest first.
func ListAnnotationRevisions(ctx context.Context, db *sql.DB, annotationID string) ([]AnnotationRevision, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT revision_id, annotation_id, prev_body, prev_tags_json, prev_investigation_id,
		       edited_at, edit_kind, edit_reason
		FROM annotation_revisions
		WHERE annotation_id = ?
		ORDER BY edited_at ASC, revision_id ASC`, annotationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnnotationRevision{}
	for rows.Next() {
		var r AnnotationRevision
		if err := rows.Scan(&r.RevisionID, &r.AnnotationID, &r.PrevBody, &r.PrevTagsJSON,
			&r.PrevInvestigationID, &r.EditedAt, &r.EditKind, &r.EditReason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateAnnotationBody replaces the body in-place and bumps updated_at.
// The previous body (and previous tags_json snapshot) are NOT written here —
// callers must invoke AddAnnotationRevision in their flow with edit_kind='body'
// before calling this so the revision row reflects the prior state.
func UpdateAnnotationBody(ctx context.Context, db *sql.DB, annotationID, body string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE annotations SET body = ?, updated_at = ? WHERE annotation_id = ?`,
		body, time.Now().Unix(), annotationID)
	return err
}

// UpdateAnnotationTags replaces the tags_json column. Also writes the legacy
// `tag` column to the first element (or NULL if tags is empty) for cross-version
// readers.
func UpdateAnnotationTags(ctx context.Context, db *sql.DB, annotationID string, tags []string) error {
	var tagsJSON, legacyTag sql.NullString
	if len(tags) > 0 {
		b, _ := json.Marshal(tags)
		tagsJSON = sql.NullString{String: string(b), Valid: true}
		legacyTag = sql.NullString{String: tags[0], Valid: true}
	}
	_, err := db.ExecContext(ctx, `
		UPDATE annotations SET tags_json = ?, tag = ?, updated_at = ? WHERE annotation_id = ?`,
		nullableString(tagsJSON), nullableString(legacyTag), time.Now().Unix(), annotationID)
	return err
}

// MoveAnnotation re-targets an annotation to a different investigation.
func MoveAnnotation(ctx context.Context, db *sql.DB, annotationID, newInvestigationID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE annotations SET investigation_id = ?, updated_at = ? WHERE annotation_id = ?`,
		newInvestigationID, time.Now().Unix(), annotationID)
	return err
}

// WithdrawAnnotation soft-deletes an annotation. Idempotent on already-withdrawn rows.
func WithdrawAnnotation(ctx context.Context, db *sql.DB, annotationID, reason string) error {
	now := time.Now().Unix()
	var rsn sql.NullString
	if reason != "" {
		rsn = sql.NullString{String: reason, Valid: true}
	}
	_, err := db.ExecContext(ctx, `
		UPDATE annotations SET withdrawn_at = ?, withdrawn_reason = ?, updated_at = ?
		WHERE annotation_id = ? AND withdrawn_at IS NULL`,
		now, nullableString(rsn), now, annotationID)
	return err
}

// RestoreAnnotation clears the withdrawn_at fields. Idempotent on non-withdrawn rows.
func RestoreAnnotation(ctx context.Context, db *sql.DB, annotationID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE annotations SET withdrawn_at = NULL, withdrawn_reason = NULL, updated_at = ?
		WHERE annotation_id = ? AND withdrawn_at IS NOT NULL`,
		time.Now().Unix(), annotationID)
	return err
}

// ----- Citations -----

// AddCitation inserts a citation row.
func AddCitation(ctx context.Context, db *sql.DB, c Citation) error {
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().Unix()
	}
	_, err := db.ExecContext(ctx, `
		INSERT OR IGNORE INTO citations (source_annotation_id, target_investigation, target_annotation_id, created_at)
		VALUES (?, ?, ?, ?)`,
		c.SourceAnnotationID, c.TargetInvestigation, nullableString(c.TargetAnnotationID), c.CreatedAt)
	return err
}

// ListCitationsFromInvestigation returns citations originating from any
// annotation on the given investigation.
func ListCitationsFromInvestigation(ctx context.Context, db *sql.DB, investigationID string) ([]Citation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.citation_id, c.source_annotation_id, c.target_investigation, c.target_annotation_id, c.created_at
		FROM citations c JOIN annotations a ON a.annotation_id = c.source_annotation_id
		WHERE a.investigation_id = ?`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Citation{}
	for rows.Next() {
		var c Citation
		if err := rows.Scan(&c.CitationID, &c.SourceAnnotationID, &c.TargetInvestigation, &c.TargetAnnotationID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCitationsToInvestigation returns citations targeting the given investigation.
func ListCitationsToInvestigation(ctx context.Context, db *sql.DB, investigationID string) ([]Citation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT citation_id, source_annotation_id, target_investigation, target_annotation_id, created_at
		FROM citations WHERE target_investigation = ?`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Citation{}
	for rows.Next() {
		var c Citation
		if err := rows.Scan(&c.CitationID, &c.SourceAnnotationID, &c.TargetInvestigation, &c.TargetAnnotationID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ----- Runs -----

// InsertRun inserts a row into runs.
func InsertRun(ctx context.Context, db *sql.DB, r Run) error {
	if r.SubmittedAt == 0 {
		r.SubmittedAt = time.Now().Unix()
	}
	if r.Status == "" {
		r.Status = "running"
	}
	var gpuArg any
	if r.GpuCount.Valid {
		gpuArg = r.GpuCount.Int64
	}
	var scratchArg any
	if r.ScratchGB.Valid {
		scratchArg = r.ScratchGB.Float64
	}
	var cpuReqArg any
	if r.CPURequest.Valid {
		cpuReqArg = r.CPURequest.Float64
	}
	var memReqArg any
	if r.MemRequestGB.Valid {
		memReqArg = r.MemRequestGB.Float64
	}
	var subSrcArg any
	if r.SubmissionSource.Valid {
		subSrcArg = r.SubmissionSource.String
	}
	var tagsArg any
	if t := tagsToJSON(r.Tags); t != "" {
		tagsArg = t
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO runs (run_id, context_name, project_id, investigation_id, verb, workload_ref, workload_version, params_json, namespace, workspace, submitted_at, status, freeze_id, gpu_count, scratch_gb, cpu_request, mem_request_gb, submission_source, tags_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.ContextName, nullableString(r.ProjectID), nullableString(r.InvestigationID),
		r.Verb, r.WorkloadRef, nullableString(r.WorkloadVersion), nullableString(r.ParamsJSON),
		nullableString(r.Namespace), nullableString(r.Workspace), r.SubmittedAt, r.Status,
		nullableString(r.FreezeID), gpuArg, scratchArg,
		cpuReqArg, memReqArg, subSrcArg, tagsArg,
		time.Now().Unix())
	return err
}

// CompleteRun updates a run with completion fields.
func CompleteRun(ctx context.Context, db *sql.DB, runID, status, exitReason string, completedAt int64, cpuHours, memGBHours float64, walltimeSec int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE runs SET completed_at = ?, status = ?, exit_reason = ?, cpu_hours = ?, memory_gb_hours = ?, walltime_seconds = ?
		WHERE run_id = ?`, completedAt, status, exitReason, cpuHours, memGBHours, walltimeSec, runID)
	return err
}

// UpdateRunJobID stores the Nomad job ID for an already-inserted run.
// Called synchronously by runner.Watch() before spawning the poll goroutine
// so that ReconcileStuckRuns() can re-probe Nomad even if the goroutine died.
func UpdateRunJobID(ctx context.Context, db *sql.DB, runID, jobID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET nomad_job_id = ? WHERE run_id = ?`, jobID, runID)
	return err
}

// UpdateRunWorkdirRoot stores the stable workdir path for a pipeline run
// (the part of the s3:// workdir prefix that's identical across resumes).
// Migration 0012 added the column for the future cache aggregator that
// will join per-task cost data across a resume chain — this writer keeps
// the column populated so that aggregator can group resumes without
// requiring a schema migration later. NULL for job rows (single-alloc
// jobs don't have a workdir-keyed resume model).
func UpdateRunWorkdirRoot(ctx context.Context, db *sql.DB, runID, workdirRoot string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET workdir_root = ? WHERE run_id = ?`, workdirRoot, runID)
	return err
}

// ListRunsForInvestigation returns runs attached to an investigation.
func ListRunsForInvestigation(ctx context.Context, db *sql.DB, investigationID string) ([]Run, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT run_id, context_name, project_id, investigation_id, verb, workload_ref, workload_version, params_json, namespace, workspace, submitted_at, completed_at, status, exit_reason, cpu_hours, memory_gb_hours, walltime_seconds, gpu_count, scratch_gb, freeze_id, COALESCE(tags_json,'')
		FROM runs WHERE investigation_id = ? ORDER BY submitted_at`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var tagsJSON string
		if err := rows.Scan(&r.RunID, &r.ContextName, &r.ProjectID, &r.InvestigationID, &r.Verb,
			&r.WorkloadRef, &r.WorkloadVersion, &r.ParamsJSON, &r.Namespace, &r.Workspace,
			&r.SubmittedAt, &r.CompletedAt, &r.Status, &r.ExitReason, &r.CPUHours,
			&r.MemoryGBHours, &r.WalltimeSeconds, &r.GpuCount, &r.ScratchGB, &r.FreezeID, &tagsJSON); err != nil {
			return nil, err
		}
		r.Tags = jsonToTags(tagsJSON)
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountRunsForInvestigation returns the run count.
func CountRunsForInvestigation(ctx context.Context, db *sql.DB, investigationID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE investigation_id = ?`, investigationID).Scan(&n)
	return n, err
}

// CountRunsForProject returns the run count.
func CountRunsForProject(ctx context.Context, db *sql.DB, projectID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}

// CountRunsForWorkdirRoot returns how many recorded runs share the given
// workdir_root. Used to number resume lineages: a pipeline resumed against
// the same --work-dir reuses this root, so the count is the position in the
// lineage (the original run is 1, the first resume sees 1, etc.).
func CountRunsForWorkdirRoot(ctx context.Context, db *sql.DB, workdirRoot string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE workdir_root = ?`, workdirRoot).Scan(&n)
	return n, err
}

// LatestRunForWorkdirRoot returns the most recently submitted run recorded
// against the given workdir_root, and ok=false when none exist. Used by
// `abc pipeline run --resume` to auto-discover the revision a prior run of the
// same --work-dir used, so the resume runs identical pipeline code (and thus
// hits the cloudcache) without the user having to look it up and re-pass it.
func LatestRunForWorkdirRoot(ctx context.Context, db *sql.DB, workdirRoot string) (Run, bool, error) {
	row := db.QueryRowContext(ctx, `
		SELECT run_id, workload_ref, workload_version, submitted_at, nomad_job_id
		FROM runs
		WHERE workdir_root = ?
		ORDER BY submitted_at DESC, rowid DESC
		LIMIT 1`, workdirRoot)
	var r Run
	err := row.Scan(&r.RunID, &r.WorkloadRef, &r.WorkloadVersion, &r.SubmittedAt, &r.NomadJobID)
	if err == sql.ErrNoRows {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	return r, true, nil
}
