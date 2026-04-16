package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacySecretsAndCrypt(t *testing.T) {
	raw := []byte(`version: "1"
active_context: dev
secrets:
  k1: encval
defaults:
  crypt_password: pw
  crypt_salt: sl
contexts:
  dev:
    endpoint: "https://example"
  other:
    endpoint: "https://other"
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
	dev := cfg.Contexts["dev"]
	if got := dev.Secrets["k1"]; got != "encval" {
		t.Fatalf("migrated secret: got %q", got)
	}
	if dev.Crypt.Password != "pw" || dev.Crypt.Salt != "sl" {
		t.Fatalf("migrated crypt: password=%q salt=%q", dev.Crypt.Password, dev.Crypt.Salt)
	}
	if _, ok := cfg.Contexts["other"].Secrets["k1"]; ok {
		t.Fatal("legacy secrets should not merge into non-target context")
	}
}

func TestContextForSecrets_ActiveAndSole(t *testing.T) {
	c := &Config{ActiveContext: "a", Contexts: map[string]Context{"a": {Endpoint: "x"}}}
	name, _, err := c.ContextForSecrets()
	if err != nil || name != "a" {
		t.Fatalf("active: name=%q err=%v", name, err)
	}
	c2 := &Config{Contexts: map[string]Context{"only": {Endpoint: "x"}}}
	name2, _, err2 := c2.ContextForSecrets()
	if err2 != nil || name2 != "only" {
		t.Fatalf("sole: name=%q err=%v", name2, err2)
	}
	c3 := &Config{Contexts: map[string]Context{"a": {}, "b": {}}}
	if _, _, err3 := c3.ContextForSecrets(); err3 == nil {
		t.Fatal("expected error when multiple contexts and no active")
	}
}
