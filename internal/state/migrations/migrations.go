// Package migrations holds embedded SQL migration files and the driver that
// applies them in lexicographic order under an exclusive lock.
//
// Schema version is recorded in the schema_migrations table; one row per
// applied migration, with the CLI version that applied it.
//
// On every state.Open(), Apply() runs and:
//
//  1. If the DB has any applied migration NOT in the embedded set, returns
//     ErrSchemaAhead — the binary is older than the DB. The caller (typically
//     `abc cache status` or any DB-backed verb) prints a clear message asking
//     the user to upgrade `abc`.
//  2. Backs up the DB file to <path>.backup-pre-<version>-<unix> before
//     applying any pending migration. Keeps the most recent 5 backups.
//  3. Applies pending migrations in lexicographic order, each in its own
//     IMMEDIATE transaction; records (version, applied_at, applied_by_cli_version).
//
// Migrations are forward-only. To change the schema, add NNNN_description.sql
// where NNNN is one greater than the current highest. Never edit an applied
// migration file.
package migrations

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed *.sql
var fsys embed.FS

// ErrSchemaAhead is returned when the DB has applied migrations that this
// binary does not know about. The CLI must be upgraded before this DB can
// be opened safely.
var ErrSchemaAhead = errors.New("schema ahead of binary: upgrade abc CLI")

// maxBackups is the number of pre-migration backups retained.
const maxBackups = 5

// Apply runs every embedded migration not yet recorded in schema_migrations.
//
// dbPath is the on-disk file path; needed for pre-migration backup. Pass
// the empty string to skip backup (in-memory test DBs).
//
// cliVersion is recorded against each newly-applied migration row.
func Apply(db *sql.DB, dbPath, cliVersion string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version                 TEXT PRIMARY KEY,
		applied_at              INTEGER NOT NULL,
		applied_by_cli_version  TEXT
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	// Defensive ALTER for DBs created before the applied_by_cli_version
	// column existed. SQLite errors with "duplicate column" if it's already
	// there; we swallow that one case.
	if _, err := db.Exec(`ALTER TABLE schema_migrations ADD COLUMN applied_by_cli_version TEXT`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return fmt.Errorf("alter schema_migrations: %w", err)
		}
	}

	embedded, err := List()
	if err != nil {
		return err
	}
	embeddedSet := map[string]bool{}
	for _, v := range embedded {
		embeddedSet[v] = true
	}

	applied, err := readApplied(db)
	if err != nil {
		return err
	}

	// Future-migration check: any applied version not in the embedded set
	// means the DB is ahead of this binary.
	var future []string
	for v := range applied {
		if !embeddedSet[v] {
			future = append(future, v)
		}
	}
	if len(future) > 0 {
		sort.Strings(future)
		return fmt.Errorf("%w: DB has %d migration(s) this binary does not know about: %s",
			ErrSchemaAhead, len(future), strings.Join(future, ", "))
	}

	// Determine pending migrations.
	var pending []string
	for _, v := range embedded {
		if !applied[v] {
			pending = append(pending, v)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	// Backup once before the first pending migration runs.
	if dbPath != "" {
		if err := backup(dbPath, pending[0]); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
		if err := pruneBackups(dbPath, maxBackups); err != nil {
			fmt.Fprintf(os.Stderr, "[abc] warning: prune old DB backups: %v\n", err)
		}
	}

	for _, version := range pending {
		body, err := fs.ReadFile(fsys, version+".sql")
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w (a backup was written; restore from %s.backup-pre-%s-* if needed)",
				version, err, dbPath, pending[0])
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, applied_at, applied_by_cli_version) VALUES (?, ?, ?)`,
			version, time.Now().Unix(), cliVersion,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

// List returns embedded migration versions in lexicographic order.
func List() ([]string, error) {
	files, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		out = append(out, strings.TrimSuffix(f.Name(), ".sql"))
	}
	sort.Strings(out)
	return out, nil
}

// AppliedVersions returns versions recorded in schema_migrations, sorted ascending.
// Used by `abc cache status`.
func AppliedVersions(db *sql.DB) ([]Applied, error) {
	rows, err := db.Query(`SELECT version, applied_at, COALESCE(applied_by_cli_version, '') FROM schema_migrations ORDER BY version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Applied
	for rows.Next() {
		var a Applied
		if err := rows.Scan(&a.Version, &a.AppliedAt, &a.AppliedByCLIVersion); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Applied is one row from schema_migrations.
type Applied struct {
	Version             string
	AppliedAt           int64
	AppliedByCLIVersion string
}

// Pending returns embedded versions not yet applied to the given DB.
func Pending(db *sql.DB) ([]string, error) {
	embedded, err := List()
	if err != nil {
		return nil, err
	}
	applied, err := readApplied(db)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range embedded {
		if !applied[v] {
			out = append(out, v)
		}
	}
	return out, nil
}

// Future returns applied versions that this binary's embedded migrations do
// not include. Non-empty result means the DB is ahead of the binary.
func Future(db *sql.DB) ([]string, error) {
	embedded, err := List()
	if err != nil {
		return nil, err
	}
	embeddedSet := map[string]bool{}
	for _, v := range embedded {
		embeddedSet[v] = true
	}
	applied, err := readApplied(db)
	if err != nil {
		return nil, err
	}
	var out []string
	for v := range applied {
		if !embeddedSet[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

func readApplied(db *sql.DB) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// backup copies the DB file (literal byte-for-byte) to a timestamped
// pre-migration backup. WAL frames not yet checkpointed live in the
// adjacent -wal file; we don't copy that here on the assumption that the
// caller has already opened the DB (which checkpoints) before this call.
// Best-effort: failure is surfaced to the caller.
func backup(dbPath, beforeVersion string) error {
	src, err := os.Open(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			// First-run: nothing to back up.
			return nil
		}
		return err
	}
	defer src.Close()

	stamp := fmt.Sprintf("%s.backup-pre-%s-%d", dbPath, beforeVersion, time.Now().Unix())
	dst, err := os.OpenFile(stamp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(stamp)
		return err
	}
	return dst.Close()
}

// pruneBackups keeps the most recent `keep` backups for the given DB and
// removes older ones.
func pruneBackups(dbPath string, keep int) error {
	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath) + ".backup-pre-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type entry struct {
		name string
		mod  time.Time
	}
	var matches []entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), base) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		matches = append(matches, entry{e.Name(), info.ModTime()})
	}
	if len(matches) <= keep {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].mod.After(matches[j].mod)
	})
	for _, m := range matches[keep:] {
		_ = os.Remove(filepath.Join(dir, m.name))
	}
	return nil
}
