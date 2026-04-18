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

func TestPerContextAliasField(t *testing.T) {
	raw := []byte(`version: "1"
active_context: lab
contexts:
  min:
    alias: lab
    endpoint: https://min.example
    access_token: tok-min
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
	if !cfg.HasDefinedContext("lab") {
		t.Fatal("expected lab to resolve")
	}
	if got := cfg.ResolveContextName("lab"); got != "min" {
		t.Fatalf("ResolveContextName(lab)=%q want min", got)
	}
	ctx, ok := cfg.ContextNamed("lab")
	if !ok || ctx.Endpoint != "https://min.example" {
		t.Fatalf("ContextNamed(lab): ok=%v endpoint=%q", ok, ctx.Endpoint)
	}
	als := AliasesResolvingToCanon(cfg, "min")
	if len(als) != 1 || als[0] != "lab" {
		t.Fatalf("aliases for min: %#v", als)
	}
}

func TestSaveRejectsAliasKeyEqualToPrimaryName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	c := &Config{
		Version:        CurrentVersion,
		Contexts:       map[string]Context{"min": {Endpoint: "https://x", AccessToken: "t"}},
		ContextAliases: map[string]string{"lab": "min", "min": "other"},
	}
	if err := c.SaveTo(path); err == nil {
		t.Fatal("expected error when a primary context name is also an alias key")
	}
}

func TestResolveContextNamePrimaryBeatsAliasKey(t *testing.T) {
	c := &Config{
		Contexts:       map[string]Context{"min": {Endpoint: "https://x"}},
		ContextAliases: map[string]string{"min": "should-not-follow"},
	}
	if got := c.ResolveContextName("min"); got != "min" {
		t.Fatalf("ResolveContextName(min)=%q want min", got)
	}
}

func TestPerContextAliasConflict(t *testing.T) {
	raw := []byte(`version: "1"
contexts:
  a:
    aliases: [shared]
    endpoint: https://a.example
    access_token: x
  b:
    aliases: [shared]
    endpoint: https://b.example
    access_token: y
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for duplicate alias")
	}
}
