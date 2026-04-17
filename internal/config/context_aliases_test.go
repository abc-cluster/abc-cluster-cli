package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadContextAliasScalar(t *testing.T) {
	raw := []byte(`version: "1"
active_context: default
contexts:
  default: aither
  aither:
    endpoint: https://a.example
    access_token: tok-a
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextAliases["default"] != "aither" {
		t.Fatalf("alias: %+v", cfg.ContextAliases)
	}
	ctx, ok := cfg.ContextNamed("default")
	if !ok || ctx.Endpoint != "https://a.example" {
		t.Fatalf("ContextNamed(default): ok=%v endpoint=%q", ok, ctx.Endpoint)
	}
	if cfg.ActiveCtx().AccessToken != "tok-a" {
		t.Fatalf("ActiveCtx token: %q", cfg.ActiveCtx().AccessToken)
	}
}

func TestContextForSecrets_WithAliasActive(t *testing.T) {
	c := &Config{
		ActiveContext:  "default",
		ContextAliases: map[string]string{"default": "aither"},
		Contexts:       map[string]Context{"aither": {Endpoint: "x", Secrets: map[string]string{"k": "v"}}},
	}
	name, ctx, err := c.ContextForSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if name != "aither" {
		t.Fatalf("canonical name: got %q want aither", name)
	}
	if ctx.Secrets["k"] != "v" {
		t.Fatalf("secrets on canonical context")
	}
}
