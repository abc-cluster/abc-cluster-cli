package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runAppCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestInit_StreamlitWithDockerfile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, _, err := runAppCmd(t, "init", "--framework", "streamlit",
		"--name", "demo", "--project", "demo", "--with-dockerfile")
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	yaml, err := os.ReadFile(filepath.Join(dir, "abc-app.yaml"))
	if err != nil {
		t.Fatalf("abc-app.yaml not written: %v", err)
	}
	for _, frag := range []string{"name: demo", "framework: streamlit", "port: 8501"} {
		if !strings.Contains(string(yaml), frag) {
			t.Errorf("abc-app.yaml missing %q", frag)
		}
	}

	df, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("Dockerfile not written: %v", err)
	}
	if !strings.Contains(string(df), "--server.address=0.0.0.0") {
		t.Errorf("Dockerfile missing bind-contract flag:\n%s", df)
	}
}

func TestInit_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, _, err := runAppCmd(t, "init", "--name", "a", "--project", "p"); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	_, _, err := runAppCmd(t, "init", "--name", "a", "--project", "p")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite refusal, got: %v", err)
	}
	// --force succeeds.
	if _, _, err := runAppCmd(t, "init", "--name", "a", "--project", "p", "--force"); err != nil {
		t.Fatalf("init --force failed: %v", err)
	}
}

func TestInit_RejectsUnknownFramework(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, _, err := runAppCmd(t, "init", "--framework", "dash")
	if err == nil || !strings.Contains(err.Error(), "scaffoldable") {
		t.Fatalf("expected dash to be rejected for init, got: %v", err)
	}
}

func TestValidate_PrintsResolvedValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc-app.yaml")
	if err := os.WriteFile(p, []byte(sucuriYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAppCmd(t, "validate", "-f", p)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	for _, frag := range []string{"is valid", "port        8085", "app-abc-platform-sucuri"} {
		if !strings.Contains(out, frag) {
			t.Errorf("validate output missing %q:\n%s", frag, out)
		}
	}
}

func TestValidate_RejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc-app.yaml")
	if err := os.WriteFile(p, []byte("name: a\nframework: pode\nproject: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runAppCmd(t, "validate", "-f", p)
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected image-required error, got: %v", err)
	}
}
