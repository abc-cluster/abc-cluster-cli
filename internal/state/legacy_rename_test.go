package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLegacyFilename_HappyPath: an existing ~/.abc/state.db (legacy
// filename) plus its WAL/SHM sidecars and one backup file are moved to the
// canonical local.db filename when DefaultPath is called and the canonical
// file does not yet exist.
func TestMigrateLegacyFilename_HappyPath(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, DefaultDBFilename)

	// Seed the legacy files.
	legacy := filepath.Join(dir, LegacyDBFilename)
	for _, f := range []string{legacy, legacy + "-wal", legacy + "-shm"} {
		if err := os.WriteFile(f, []byte("seed"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	backup := filepath.Join(dir, LegacyDBFilename+".backup-pre-0001_initial-1700000000")
	if err := os.WriteFile(backup, []byte("seedbackup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := migrateLegacyFilename(dir, canonical); err != nil {
		t.Fatalf("migrateLegacyFilename: %v", err)
	}

	// Canonical file + sidecars present at new name.
	for _, f := range []string{canonical, canonical + "-wal", canonical + "-shm"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected %s to exist after rename, got: %v", f, err)
		}
	}
	// Legacy files gone.
	for _, f := range []string{legacy, legacy + "-wal", legacy + "-shm"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("expected %s to be gone, stat err: %v", f, err)
		}
	}
	// Backup renamed.
	newBackup := filepath.Join(dir, DefaultDBFilename+".backup-pre-0001_initial-1700000000")
	if _, err := os.Stat(newBackup); err != nil {
		t.Errorf("expected backup to be renamed; stat %s err: %v", newBackup, err)
	}
}

// TestMigrateLegacyFilename_NoOp: a fresh install (neither file exists)
// leaves the directory untouched.
func TestMigrateLegacyFilename_NoOp(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, DefaultDBFilename)
	if err := migrateLegacyFilename(dir, canonical); err != nil {
		t.Fatalf("migrateLegacyFilename on empty dir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty dir after no-op migrate; got %d entries", len(entries))
	}
}

// TestMigrateLegacyFilename_CanonicalAlreadyPresent: when ~/.abc/local.db
// exists, the legacy file is left alone (no surprise overwrites).
func TestMigrateLegacyFilename_CanonicalAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, DefaultDBFilename)
	if err := os.WriteFile(canonical, []byte("canonical"), 0o600); err != nil {
		t.Fatalf("seed canonical: %v", err)
	}
	legacy := filepath.Join(dir, LegacyDBFilename)
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := migrateLegacyFilename(dir, canonical); err != nil {
		t.Fatalf("migrateLegacyFilename: %v", err)
	}
	// Both files still present.
	if data, _ := os.ReadFile(canonical); string(data) != "canonical" {
		t.Errorf("canonical file mutated; got %q", data)
	}
	if data, _ := os.ReadFile(legacy); string(data) != "legacy" {
		t.Errorf("legacy file mutated; got %q", data)
	}
}
