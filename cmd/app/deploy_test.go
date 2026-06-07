package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSpec writes an abc-app.yaml into a temp dir and returns its path.
func writeSpec(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "abc-app.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sucuriYAML = `name: sucuri
framework: pode
image: ghcr.io/biosharp-dotnet/sucuri-api:latest
port: 8085
health: /health/ready
access: team
project: abc-platform
env:
  SUCURI_ENVIRONMENT: production
  SUCURI_LOG_LEVEL: Warning
resources:
  cpu: 500
  memory: 256
`

// TestDeployDryRun_NoSideEffects verifies --dry-run renders HCL to stdout and
// submits nothing (no Nomad/MinIO interaction — the spec has no data buckets).
func TestDeployDryRun_NoSideEffects(t *testing.T) {
	specPath := writeSpec(t, sucuriYAML)

	root := NewCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"deploy", "-f", specPath, "--dry-run"})

	if err := root.Execute(); err != nil {
		t.Fatalf("dry-run deploy failed: %v\nstderr: %s", err, errOut.String())
	}
	got := out.String()
	for _, frag := range []string{
		`job "app-abc-platform-sucuri"`,
		`provider = "nomad"`,
		"traefik.enable=true",
		"Host(`abc-platform-sucuri.apps.seedling.abc-cluster.cloud`)",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("dry-run output missing %q:\n%s", frag, got)
		}
	}
}

// TestDeploy_RejectsMissingImage exercises the validation gate via the CLI.
func TestDeploy_RejectsMissingImage(t *testing.T) {
	specPath := writeSpec(t, "name: a\nframework: pode\nproject: p\n")
	root := NewCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"deploy", "-f", specPath, "--dry-run"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected image-required error, got: %v", err)
	}
}

// TestDeploy_RejectsPhase2Framework confirms dash is rejected via the CLI.
func TestDeploy_RejectsPhase2Framework(t *testing.T) {
	specPath := writeSpec(t,
		"name: a\nframework: dash\nimage: ghcr.io/o/a:1\nproject: p\n")
	root := NewCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"deploy", "-f", specPath, "--dry-run"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("expected phase-2 framework rejection, got: %v", err)
	}
}

// TestDeploy_ImageOverride confirms --image overrides the yaml in dry-run.
func TestDeploy_ImageOverride(t *testing.T) {
	specPath := writeSpec(t, sucuriYAML)
	root := NewCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"deploy", "-f", specPath, "--dry-run",
		"--image", "ghcr.io/biosharp-dotnet/sucuri-api:v2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !strings.Contains(out.String(), "sucuri-api:v2") {
		t.Errorf("--image override not reflected:\n%s", out.String())
	}
}
