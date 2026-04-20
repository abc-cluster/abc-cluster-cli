package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthWhoamiSetGetUnset(t *testing.T) {
	t.Parallel()
	c := &Config{
		Version:       CurrentVersion,
		ActiveContext: "lab",
		Contexts: map[string]Context{
			"lab": {Endpoint: "https://example.invalid"},
		},
	}
	if err := c.Set("contexts.lab.auth.whoami", "token-user"); err != nil {
		t.Fatal(err)
	}
	v, ok := c.Get("contexts.lab.auth.whoami")
	if !ok || v != "token-user" {
		t.Fatalf("get: ok=%v v=%q", ok, v)
	}
	if err := c.Unset("contexts.lab.auth.whoami"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("contexts.lab.auth.whoami"); ok {
		t.Fatal("expected whoami unset")
	}
}

func TestLoadFromAuthScalarRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := `version: "1"
active_context: boot
contexts:
  boot:
    endpoint: https://example.invalid
    access_token: ""
    cluster_type: abc-nodes
    auth: root
    admin:
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: bootstrap
          nomad_region: global
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cfg.Contexts["boot"]
	if ctx.Auth == nil || !ctx.Auth.Root {
		t.Fatalf("auth: root: Auth=%+v", ctx.Auth)
	}
	v, ok := cfg.Get("contexts.boot.auth.root")
	if !ok || v != "true" {
		t.Fatalf("get auth.root: ok=%v v=%q", ok, v)
	}
}

func TestAuthWhoamiUnsetPreservesRoot(t *testing.T) {
	t.Parallel()
	c := &Config{
		Version:       CurrentVersion,
		ActiveContext: "lab",
		Contexts: map[string]Context{
			"lab": {
				Endpoint: "https://example.invalid",
				Auth:     &ContextAuth{Root: true, Whoami: "mgmt"},
			},
		},
	}
	if err := c.Set("contexts.lab.auth.whoami", ""); err != nil {
		t.Fatal(err)
	}
	ctx := c.Contexts["lab"]
	if ctx.Auth == nil || !ctx.Auth.Root {
		t.Fatalf("expected Root preserved, Auth=%+v", ctx.Auth)
	}
	if ctx.Auth.Whoami != "" {
		t.Fatalf("whoami should be empty")
	}
}
