package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_ABCActiveContextOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `version: "1"
active_context: other
contexts:
  other:
    endpoint: https://other.example
    access_token: tok-other
  aither:
    endpoint: https://aither.example
    access_token: tok-aither
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_ACTIVE_CONTEXT", "aither")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveContext != "aither" {
		t.Fatalf("active context: got %q want aither", cfg.ActiveContext)
	}
	if cfg.ActiveCtx().Endpoint != "https://aither.example" {
		t.Fatalf("endpoint: %q", cfg.ActiveCtx().Endpoint)
	}
}

func TestLoadFrom_ABCActiveContextUnknownIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`version: "1"
active_context: x
contexts:
  x:
    endpoint: https://x.example
    access_token: t
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_ACTIVE_CONTEXT", "missing")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveContext != "x" {
		t.Fatalf("unknown ABC_ACTIVE_CONTEXT must not override file; got %q want x", cfg.ActiveContext)
	}
}
