// Package state owns the local SQLite database at ~/.abc/db/local.db.
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
	// DefaultDBFilename is the canonical DB filename.
	DefaultDBFilename = "local.db"

	// DefaultDBDir is the subdirectory under ~/.abc that holds the database
	// (since 2026-06-02). The DB now lives at ~/.abc/db/local.db so the home
	// stays tidy (config.yaml, db/, .secrets, …) rather than scattering DB
	// files at the top level.
	DefaultDBDir = "db"

	// LegacyDBFilename is the pre-2026-05-08 filename. Migrated to
	// DefaultDBFilename automatically on first Open() if present and the
	// canonical file is absent.
	LegacyDBFilename = "state.db"
)

var (
	openMu sync.Mutex
	cache  = map[string]*sql.DB{}
)

// DefaultPath returns ~/.abc/db/local.db, creating the db/ dir if needed.
// Transparent migrations on first call (each runs only if the canonical file
// is absent):
//   1. flat ~/.abc/state.db  → ~/.abc/local.db   (pre-2026-05-08 rename)
//   2. flat ~/.abc/local.db  → ~/.abc/db/local.db (2026-06-02 move into db/)
// Both carry WAL/SHM sidecars and matching backup files along.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	abcDir := filepath.Join(home, ".abc")
	dbDir := filepath.Join(abcDir, DefaultDBDir)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dbDir, err)
	}
	canonical := filepath.Join(dbDir, DefaultDBFilename)

	// Step 1: legacy state.db → local.db, still at the flat ~/.abc level.
	flatLocal := filepath.Join(abcDir, DefaultDBFilename)
	if err := migrateLegacyFilename(abcDir, flatLocal); err != nil {
		return "", fmt.Errorf("migrate legacy %s → %s: %w", LegacyDBFilename, DefaultDBFilename, err)
	}
	// Step 2: flat ~/.abc/local.db → ~/.abc/db/local.db.
	if err := migrateIntoDBDir(abcDir, dbDir, canonical); err != nil {
		return "", fmt.Errorf("migrate %s → db/: %w", DefaultDBFilename, err)
	}
	return canonical, nil
}

// migrateIntoDBDir moves a flat ~/.abc/local.db (+ WAL/SHM + backups) into
// ~/.abc/db/local.db. No-op if the canonical file already exists or no flat
// file is present.
func migrateIntoDBDir(abcDir, dbDir, canonical string) error {
	if _, err := os.Stat(canonical); err == nil {
		return nil // already in db/ — nothing to do
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", canonical, err)
	}
	flat := filepath.Join(abcDir, DefaultDBFilename)
	if _, err := os.Stat(flat); err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install — nothing to move
		}
		return fmt.Errorf("stat %s: %w", flat, err)
	}
	// Move main DB + WAL + SHM as a set.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := flat + suffix
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
	// Best-effort: move local.db.backup-pre-* backups into db/.
	if entries, err := os.ReadDir(abcDir); err == nil {
		const prefix = DefaultDBFilename + ".backup-pre-"
		for _, e := range entries {
			name := e.Name()
			if len(name) > len(prefix) && name[:len(prefix)] == prefix {
				_ = os.Rename(filepath.Join(abcDir, name), filepath.Join(dbDir, name)) // best-effort
			}
		}
	}
	fmt.Fprintf(os.Stderr, "[abc] note: moved ~/.abc/%s → ~/.abc/%s/%s (one-shot; keeps the home tidy).\n",
		DefaultDBFilename, DefaultDBDir, DefaultDBFilename)
	return nil
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

// Open opens (or creates) ~/.abc/db/local.db, applies PRAGMAs, runs pending
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
