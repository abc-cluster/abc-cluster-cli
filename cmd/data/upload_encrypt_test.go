package data

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadTempDir_DefaultsToHomeDotAbc(t *testing.T) {
	t.Setenv("ABC_CLI_TMPDIR", "") // ensure env var is not set
	dir, err := uploadTempDir()
	if err != nil {
		t.Fatalf("uploadTempDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".abc", "tmpdir")
	if dir != want {
		t.Fatalf("uploadTempDir() = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to be created at %q", dir)
	}
}

func TestUploadTempDir_RespectsEnvVar(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-tmpdir")
	t.Setenv("ABC_CLI_TMPDIR", custom)
	dir, err := uploadTempDir()
	if err != nil {
		t.Fatalf("uploadTempDir: %v", err)
	}
	if dir != custom {
		t.Fatalf("uploadTempDir() = %q, want %q", dir, custom)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to be created at %q", dir)
	}
}
