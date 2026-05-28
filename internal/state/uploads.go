package state

import (
	"database/sql"
	"time"
)

// Upload is one row from the uploads table. Populated by `abc data upload`
// on each successful file transfer; queried by `abc data uploads {list,show,path}`.
type Upload struct {
	ID           int64
	Filename     string    // basename of the uploaded file
	S3Path       string    // canonical s3://<bucket>/user/<user>/data/<filename>; "" if unknown
	OriginalPath string    // local filesystem path at upload time (informational)
	SizeBytes    int64     // bytes transferred (post-encryption when --crypt was used)
	Checksum     string    // "sha256:<hex>" or "" when --checksum=false
	ContextName  string    // active context name at upload time
	UploadedAt   time.Time // UTC
}

// RecordUpload inserts a completed upload. Returns the new row ID.
// Best-effort: callers should not treat a failure as fatal since the upload
// already succeeded; the registry is a convenience index, not a hard requirement.
func RecordUpload(db *sql.DB, u Upload) (int64, error) {
	res, err := db.Exec(
		`INSERT INTO uploads
		 (filename, s3_path, original_path, size_bytes, checksum, context_name, uploaded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Filename, u.S3Path, u.OriginalPath, u.SizeBytes, u.Checksum,
		u.ContextName, u.UploadedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListUploads returns uploads ordered newest-first.
// When contextName is non-empty only that context's rows are returned.
func ListUploads(db *sql.DB, contextName string) ([]Upload, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const cols = `id, filename, s3_path, original_path, size_bytes, checksum, context_name, uploaded_at`
	if contextName != "" {
		rows, err = db.Query(
			`SELECT `+cols+` FROM uploads WHERE context_name = ? ORDER BY uploaded_at DESC`,
			contextName,
		)
	} else {
		rows, err = db.Query(`SELECT ` + cols + ` FROM uploads ORDER BY uploaded_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, scanErr := scanUploadsRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FindUploadByFilename returns the most recent upload matching filename.
// When contextName is non-empty the search is restricted to that context.
// Returns ErrNotFound when no matching row exists.
func FindUploadByFilename(db *sql.DB, filename, contextName string) (Upload, error) {
	const cols = `id, filename, s3_path, original_path, size_bytes, checksum, context_name, uploaded_at`
	var row *sql.Row
	if contextName != "" {
		row = db.QueryRow(
			`SELECT `+cols+` FROM uploads WHERE filename = ? AND context_name = ? ORDER BY uploaded_at DESC LIMIT 1`,
			filename, contextName,
		)
	} else {
		row = db.QueryRow(
			`SELECT `+cols+` FROM uploads WHERE filename = ? ORDER BY uploaded_at DESC LIMIT 1`,
			filename,
		)
	}
	return scanUploadSingleRow(row)
}

// scanUploadsRow scans one row from a *sql.Rows cursor.
func scanUploadsRow(rows *sql.Rows) (Upload, error) {
	var u Upload
	var uploadedAt string
	if err := rows.Scan(
		&u.ID, &u.Filename, &u.S3Path, &u.OriginalPath,
		&u.SizeBytes, &u.Checksum, &u.ContextName, &uploadedAt,
	); err != nil {
		return Upload{}, err
	}
	u.UploadedAt, _ = time.Parse(time.RFC3339, uploadedAt)
	return u, nil
}

// scanUploadSingleRow scans a *sql.Row (single-row query result).
func scanUploadSingleRow(row *sql.Row) (Upload, error) {
	var u Upload
	var uploadedAt string
	if err := row.Scan(
		&u.ID, &u.Filename, &u.S3Path, &u.OriginalPath,
		&u.SizeBytes, &u.Checksum, &u.ContextName, &uploadedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return Upload{}, ErrNotFound
		}
		return Upload{}, err
	}
	u.UploadedAt, _ = time.Parse(time.RFC3339, uploadedAt)
	return u, nil
}
