package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ── Criterion C: abc-reserved bucket auto-create ──────────────────────────────

// TestEnsureBucket_SuccessPath verifies that ensureBucket calls "mb" when "ls"
// fails (bucket absent) and prints the expected log message.
func TestEnsureBucket_SuccessPath(t *testing.T) {
	// Build a fake s5cmd binary that returns 1 for ls and 0 for mb.
	fakeS5cmd := buildFakeS5cmd(t, func(args []string) int {
		for _, a := range args {
			if a == "ls" {
				return 1 // bucket not found
			}
			if a == "mb" {
				return 0 // creation succeeds
			}
		}
		return 0
	})

	var buf bytes.Buffer
	err := ensureBucket(context.Background(), &buf, fakeS5cmd, "http://localhost:9000", nil, "abc-reserved")
	if err != nil {
		t.Fatalf("ensureBucket returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "abc-reserved") {
		t.Errorf("output does not mention bucket name: %q", out)
	}
	// Should print "done" after creating.
	if !strings.Contains(out, "done") {
		t.Errorf("output does not contain 'done': %q", out)
	}
}

// TestEnsureBucket_AlreadyExists verifies that ensureBucket is a no-op when
// "ls" succeeds (bucket already exists) and prints "exists".
func TestEnsureBucket_AlreadyExists(t *testing.T) {
	fakeS5cmd := buildFakeS5cmd(t, func(args []string) int {
		return 0 // everything succeeds
	})

	var buf bytes.Buffer
	err := ensureBucket(context.Background(), &buf, fakeS5cmd, "http://localhost:9000", nil, "abc-reserved")
	if err != nil {
		t.Fatalf("ensureBucket returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "exists") {
		t.Errorf("expected 'exists' in output: %q", buf.String())
	}
}

// TestEnsureBucket_FailurePath verifies that ensureBucket returns an error
// with the expected message when bucket creation fails.
func TestEnsureBucket_FailurePath(t *testing.T) {
	fakeS5cmd := buildFakeS5cmd(t, func(args []string) int {
		return 1 // everything fails
	})

	var buf bytes.Buffer
	err := ensureBucket(context.Background(), &buf, fakeS5cmd, "http://localhost:9000", nil, "abc-reserved")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "abc-reserved") || !strings.Contains(err.Error(), "http://localhost:9000") {
		t.Errorf("error message should contain bucket and endpoint: %v", err)
	}
}

// ── Criterion D: credential resolution order ─────────────────────────────────

// TestResolveS3Credentials_EnvVarWins verifies step 1: AWS_* env vars from the
// caller's environment take precedence over everything else.
func TestResolveS3Credentials_EnvVarWins(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-ak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-sk")

	ctx := config.Context{
		Admin: config.Admin{
			Services: config.AdminServices{
				MinIO: &config.AdminFloorService{
					AccessKey: "ctx-ak",
					SecretKey: "ctx-sk",
				},
			},
		},
	}
	got := resolveS3Credentials(ctx, "minio", "http://ep:9000")
	if got["AWS_ACCESS_KEY_ID"] != "env-ak" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want env-ak", got["AWS_ACCESS_KEY_ID"])
	}
	if got["AWS_SECRET_ACCESS_KEY"] != "env-sk" {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q, want env-sk", got["AWS_SECRET_ACCESS_KEY"])
	}
}

// TestResolveS3Credentials_ContextConfigWins verifies step 2: when env vars are
// absent, admin.services.minio.{access_key,secret_key} are used (no IsABCNodesCluster gate).
func TestResolveS3Credentials_ContextConfigWins(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	ctx := config.Context{
		Admin: config.Admin{
			Services: config.AdminServices{
				MinIO: &config.AdminFloorService{
					AccessKey: "ctx-ak",
					SecretKey: "ctx-sk",
				},
			},
			ABCNodes: &config.AdminABCNodes{
				S3AccessKey: "nodes-ak",
				S3SecretKey: "nodes-sk",
			},
		},
	}
	got := resolveS3Credentials(ctx, "minio", "http://ep:9000")
	if got["AWS_ACCESS_KEY_ID"] != "ctx-ak" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want ctx-ak", got["AWS_ACCESS_KEY_ID"])
	}
	if got["AWS_SECRET_ACCESS_KEY"] != "ctx-sk" {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q, want ctx-sk", got["AWS_SECRET_ACCESS_KEY"])
	}
}

// TestResolveS3Credentials_AbcNodesFallback verifies step 3: when env vars and
// admin.services.minio are both absent, admin.abc_nodes fallback is used.
func TestResolveS3Credentials_AbcNodesFallback(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	ctx := config.Context{
		Admin: config.Admin{
			ABCNodes: &config.AdminABCNodes{
				S3AccessKey: "nodes-ak",
				S3SecretKey: "nodes-sk",
			},
		},
	}
	got := resolveS3Credentials(ctx, "minio", "http://ep:9000")
	if got["AWS_ACCESS_KEY_ID"] != "nodes-ak" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want nodes-ak", got["AWS_ACCESS_KEY_ID"])
	}
	if got["AWS_SECRET_ACCESS_KEY"] != "nodes-sk" {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q, want nodes-sk", got["AWS_SECRET_ACCESS_KEY"])
	}
}

// TestResolveS3Credentials_EndpointInjected verifies that the endpoint is always
// injected as AWS_ENDPOINT_URL / AWS_ENDPOINT_URL_S3 regardless of cred source.
func TestResolveS3Credentials_EndpointInjected(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "ak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "sk")
	ctx := config.Context{}
	got := resolveS3Credentials(ctx, "minio", "http://my-endpoint:9000")
	if got["AWS_ENDPOINT_URL"] != "http://my-endpoint:9000" {
		t.Errorf("AWS_ENDPOINT_URL = %q, want http://my-endpoint:9000", got["AWS_ENDPOINT_URL"])
	}
	if got["AWS_ENDPOINT_URL_S3"] != "http://my-endpoint:9000" {
		t.Errorf("AWS_ENDPOINT_URL_S3 = %q, want http://my-endpoint:9000", got["AWS_ENDPOINT_URL_S3"])
	}
}

// ── test helper ──────────────────────────────────────────────────────────────

// buildFakeS5cmd writes a tiny shell script that calls the exitCodeFn based on
// the subcommand (ls/mb) and returns its path. Skips if sh is unavailable.
func buildFakeS5cmd(t *testing.T, exitCodeFn func(args []string) int) string {
	t.Helper()

	// Locate sh.
	sh := ""
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		p := dir + "/sh"
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			sh = p
			break
		}
	}
	if sh == "" {
		t.Skip("sh not found in PATH; skipping ensureBucket test")
	}

	// We need a binary that exits with different codes for ls vs mb. Build two
	// temp scripts and choose the right one based on the callback.
	lsCode := exitCodeFn([]string{"ls"})
	mbCode := exitCodeFn([]string{"mb"})

	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    ls) exit %d ;;
    mb) exit %d ;;
  esac
done
exit 0
`, lsCode, mbCode)

	f, err := os.CreateTemp(t.TempDir(), "fake-s5cmd-*")
	if err != nil {
		t.Fatalf("create fake s5cmd: %v", err)
	}
	if _, err := io.WriteString(f, script); err != nil {
		t.Fatalf("write fake s5cmd: %v", err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatalf("chmod fake s5cmd: %v", err)
	}
	return f.Name()
}
