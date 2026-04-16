package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNormalizesFlatCryptIntoNested(t *testing.T) {
	raw := []byte(`version: "1"
active_context: x
contexts:
  x:
    endpoint: "http://e"
    crypt_password: alpha
    crypt_salt: beta
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
	x := cfg.Contexts["x"]
	if x.Crypt.Password != "alpha" || x.Crypt.Salt != "beta" {
		t.Fatalf("nested crypt: %+v", x.Crypt)
	}
	if x.FlatCryptPassword != "" || x.FlatCryptSalt != "" {
		t.Fatalf("flat fields should be cleared after normalize, got flat pw=%q salt=%q", x.FlatCryptPassword, x.FlatCryptSalt)
	}
}

func TestLoadNestedCryptYAML(t *testing.T) {
	raw := []byte(`version: "1"
active_context: x
contexts:
  x:
    endpoint: "http://e"
    crypt:
      password: nested-pw
      salt: nested-sl
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
	x := cfg.Contexts["x"]
	if x.Crypt.Password != "nested-pw" || x.Crypt.Salt != "nested-sl" {
		t.Fatalf("nested yaml: %+v", x.Crypt)
	}
}
