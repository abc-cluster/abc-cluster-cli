package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ── Criterion B: mc alias injection ──────────────────────────────────────────

// TestResolveMinioCredentialsFromCtx_ReadsServiceFields verifies that
// resolveMinioCredentialsFromCtx reads admin.services.minio.* fields directly
// without any IsABCNodesCluster() gate. A non-abc-nodes context (no ClusterType
// set) with admin.services.minio configured must return the correct credentials.
func TestResolveMinioCredentialsFromCtx_ReadsServiceFields(t *testing.T) {
	ctx := config.Context{
		Admin: config.Admin{
			Services: config.AdminServices{
				MinIO: &config.AdminFloorService{
					Endpoint:  "http://minio.local:9000",
					AccessKey: "myaccesskey",
					SecretKey: "mysecretkey",
				},
			},
		},
	}
	endpoint, accessKey, secretKey, err := resolveMinioCredentialsFromCtx(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint != "http://minio.local:9000" {
		t.Errorf("endpoint = %q, want %q", endpoint, "http://minio.local:9000")
	}
	if accessKey != "myaccesskey" {
		t.Errorf("accessKey = %q, want %q", accessKey, "myaccesskey")
	}
	if secretKey != "mysecretkey" {
		t.Errorf("secretKey = %q, want %q", secretKey, "mysecretkey")
	}
}

// TestResolveMinioCredentialsFromCtx_HTTPFallback verifies that the http field
// is used as the endpoint when endpoint is empty.
func TestResolveMinioCredentialsFromCtx_HTTPFallback(t *testing.T) {
	ctx := config.Context{
		Admin: config.Admin{
			Services: config.AdminServices{
				MinIO: &config.AdminFloorService{
					HTTP:      "http://minio.local:9001",
					AccessKey: "ak",
					SecretKey: "sk",
				},
			},
		},
	}
	endpoint, _, _, err := resolveMinioCredentialsFromCtx(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint != "http://minio.local:9001" {
		t.Errorf("endpoint = %q, want %q", endpoint, "http://minio.local:9001")
	}
}

// TestResolveMinioCredentialsFromCtx_UserPasswordAliases verifies that user/password
// fields are accepted as aliases for access_key/secret_key.
func TestResolveMinioCredentialsFromCtx_UserPasswordAliases(t *testing.T) {
	ctx := config.Context{
		Admin: config.Admin{
			Services: config.AdminServices{
				MinIO: &config.AdminFloorService{
					Endpoint: "http://minio.local:9000",
					User:     "miniouser",
					Password: "miniopassword",
				},
			},
		},
	}
	_, accessKey, secretKey, err := resolveMinioCredentialsFromCtx(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accessKey != "miniouser" {
		t.Errorf("accessKey = %q, want %q", accessKey, "miniouser")
	}
	if secretKey != "miniopassword" {
		t.Errorf("secretKey = %q, want %q", secretKey, "miniopassword")
	}
}

// TestMCConfigDirDoesNotTouchHomeMC verifies that runMCCommand with an
// MC_CONFIG_DIR env entry set to a temp dir does not create or modify
// ~/.mc/config.json. Uses a no-op shell command to avoid needing mc installed.
func TestMCConfigDirDoesNotTouchHomeMC(t *testing.T) {
	// Find a shell that exists on this system.
	shell, err := findShell()
	if err != nil {
		t.Skip("no shell (sh/bash) found in PATH:", err)
	}

	tmpDir := t.TempDir()

	// Snapshot ~/.mc state — it must not be created by this test.
	home, homeErr := os.UserHomeDir()
	var realMCDir string
	realMCExisted := false
	if homeErr == nil {
		realMCDir = filepath.Join(home, ".mc")
		if _, err := os.Stat(realMCDir); err == nil {
			realMCExisted = true
		}
	}

	mcEnv := append(os.Environ(), "MC_CONFIG_DIR="+tmpDir)

	// Run a trivially successful command (true or equivalent) via shell.
	// We set MC_CONFIG_DIR in env; the shell exits immediately.
	if err := runMCCommand(
		context.Background(),
		shell,
		nil,
		mcEnv,
		[]string{"-c", "exit 0"},
		nil, os.Stdout, os.Stderr,
	); err != nil {
		t.Fatalf("runMCCommand returned error: %v", err)
	}

	// Verify the real ~/.mc was not created during the run.
	if homeErr == nil && !realMCExisted {
		if _, err := os.Stat(realMCDir); err == nil {
			t.Errorf("~/.mc was created during runMCCommand; MC_CONFIG_DIR isolation failed")
		}
	}

	// Verify the MC_CONFIG_DIR temp dir itself still exists (not deleted by runMCCommand).
	if _, err := os.Stat(tmpDir); err != nil {
		t.Errorf("MC_CONFIG_DIR temp dir was removed by runMCCommand; should be caller's responsibility")
	}
}

// findShell returns the path to sh or bash.
func findShell() (string, error) {
	for _, sh := range []string{"sh", "bash"} {
		if p, err := lookPath(sh); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// lookPath is a thin wrapper so tests can call os/exec.LookPath without importing it.
func lookPath(file string) (string, error) {
	// Use PATH-based search from os.Executable dir + PATH.
	dirs := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range dirs {
		p := filepath.Join(dir, file)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			if info.Mode()&0o111 != 0 {
				return p, nil
			}
		}
	}
	return "", &os.PathError{Op: "lookPath", Path: file, Err: os.ErrNotExist}
}
