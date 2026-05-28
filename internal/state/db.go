// Package state owns the local SQLite database at ~/.abc/local.db.
//
// Renamed 2026-05-08 from ~/.abc/state.db to align with the `abc localdb`
// verb naming. Existing users with ~/.abc/state.db get a one-shot
// in-place rename on first Open() (see migrateLegacyFilename below).
//
// Single-binary, no CGO: backed by modernc.org/sqlite (pure Go).
// Concurrency: WAL + busy_timeout=5000; writes use BEGIN IMMEDIATE.
package state

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"

	_ "modernc.org/sqlite"
)

const (
	// DefaultDBFilename is the canonical filename under ~/.abc.
	DefaultDBFilename = "local.db"

	// LegacyDBFilename is the pre-2026-05-08 filename. Migrated to
	// DefaultDBFilename automatically on first Open() if present and the
	// canonical file is absent.
	LegacyDBFilename = "state.db"
)

var (
	openMu sync.Mutex
	cache  = map[string]*sql.DB{}
)

// DefaultPath returns ~/.abc/local.db, creating the parent dir if needed.
// If a legacy ~/.abc/state.db exists and the canonical file does not,
// the legacy file is renamed in place (along with its WAL/SHM sidecars
// and any matching backup files) so old installs upgrade transparently.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(home, ".abc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	canonical := filepath.Join(dir, DefaultDBFilename)
	if err := migrateLegacyFilename(dir, canonical); err != nil {
		return "", fmt.Errorf("migrate legacy %s → %s: %w", LegacyDBFilename, DefaultDBFilename, err)
	}
	return canonical, nil
}

// migrateLegacyFilename renames ~/.abc/state.db → ~/.abc/local.db (and the
// WAL / SHM sidecars; and any state.db.backup-pre-* files) when the
// canonical file does not yet exist. No-op if the canonical file is
// already present, or if no legacy file exists.
//
// Best-effort on the backup files: if those rename, great; if a per-file
// rename fails, log a warning and continue (the main DB has already moved
// and the user can resolve the backups manually).
func migrateLegacyFilename(dir, canonical string) error {
	if _, err := os.Stat(canonical); err == nil {
		// Canonical already present — nothing to do.
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", canonical, err)
	}
	legacy := filepath.Join(dir, LegacyDBFilename)
	if _, err := os.Stat(legacy); err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install — neither file exists; nothing to migrate
		}
		return fmt.Errorf("stat %s: %w", legacy, err)
	}
	// Move the main DB + WAL + SHM atomically as a set.
	suffixes := []string{"", "-wal", "-shm"}
	for _, suffix := range suffixes {
		from := legacy + suffix
		to := canonical + suffix
		if _, err := os.Stat(from); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", from, err)
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("rename %s → %s: %w", from, to, err)
		}
	}
	// Best-effort: rename pre-migration backups (state.db.backup-pre-*).
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // soft fail
	}
	for _, e := range entries {
		name := e.Name()
		const prefix = LegacyDBFilename + ".backup-pre-"
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			from := filepath.Join(dir, name)
			to := filepath.Join(dir, DefaultDBFilename+".backup-pre-"+name[len(prefix):])
			_ = os.Rename(from, to) // best-effort
		}
	}
	fmt.Fprintf(os.Stderr, "[abc] note: renamed ~/.abc/%s → ~/.abc/%s (one-shot; aligns with `abc localdb` command).\n",
		LegacyDBFilename, DefaultDBFilename)
	return nil
}

// Open opens (or creates) ~/.abc/local.db, applies PRAGMAs, runs pending
// migrations, and returns a *sql.DB. Subsequent calls with the same path
// return the cached handle. The path also auto-migrates from the
// legacy ~/.abc/state.db filename if needed (see DefaultPath).
func Open() (*sql.DB, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return OpenAt(path)
}

// OpenAt is the testable form of Open.
func OpenAt(path string) (*sql.DB, error) {
	openMu.Lock()
	defer openMu.Unlock()
	if db, ok := cache[path]; ok {
		return db, nil
	}

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// modernc.org/sqlite accepts a filename DSN. Pass busy_timeout & journal
	// via _pragma so first-open already has them.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	// Reapply PRAGMAs explicitly. Some DSN-pragma orderings are version-sensitive.
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("set %s: %w", p, err)
		}
	}
	if err := migrations.Apply(db, path, CLIVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	cache[path] = db
	return db, nil
}

// Close closes any cached DB handles. Tests use it; production is fine with
// process exit.
func Close() error {
	openMu.Lock()
	defer openMu.Unlock()
	var firstErr error
	for path, db := range cache {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(cache, path)
	}
	return firstErr
}

// ErrNotFound is returned by lookups when the requested row does not exist.
var ErrNotFound = errors.New("not found")
