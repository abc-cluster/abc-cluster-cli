// Package state owns the local SQLite database at ~/.abc/state.db.
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
	// DefaultDBFilename is the path under ~/.abc.
	DefaultDBFilename = "state.db"
)

var (
	openMu sync.Mutex
	cache  = map[string]*sql.DB{}
)

// DefaultPath returns ~/.abc/state.db, creating the parent dir if needed.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(home, ".abc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return filepath.Join(dir, DefaultDBFilename), nil
}

// Open opens (or creates) ~/.abc/state.db, applies PRAGMAs, runs pending
// migrations, and returns a *sql.DB. Subsequent calls with the same path
// return the cached handle.
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
