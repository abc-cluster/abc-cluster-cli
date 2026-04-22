package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultContext(t *testing.T) {
	c := &Config{Contexts: map[string]Context{}}
	c.EnsureDefaultContext()
	if c.ActiveContext != DefaultContextName {
		t.Fatalf("active_context: got %q want %q", c.ActiveContext, DefaultContextName)
	}
	ctx := c.Contexts[DefaultContextName]
	if ctx.Endpoint != DefaultPublicAPIEndpoint {
		t.Fatalf("endpoint: got %q want %q", ctx.Endpoint, DefaultPublicAPIEndpoint)
	}
	if ctx.UploadEndpoint == "" {
		t.Fatal("expected derived upload_endpoint")
	}
}

func TestEnsureDefaultContext_preservesExistingDefault(t *testing.T) {
	c := &Config{
		Contexts: map[string]Context{
			DefaultContextName: {Endpoint: "https://custom.example", AccessToken: "tok"},
		},
		ActiveContext: DefaultContextName,
	}
	c.EnsureDefaultContext()
	if got := c.Contexts[DefaultContextName].Endpoint; got != "https://custom.example" {
		t.Fatalf("endpoint overwritten: got %q", got)
	}
}

func TestEnsureDefaultContext_doesNotPickActiveWhenOtherContextsExist(t *testing.T) {
	c := &Config{
		Contexts: map[string]Context{
			"lab": {Endpoint: "https://x", AccessToken: "t"},
		},
		ActiveContext: "",
	}
	c.EnsureDefaultContext()
	if _, ok := c.Contexts[DefaultContextName]; !ok {
		t.Fatal("expected default context to be added")
	}
	if c.ActiveContext != "" {
		t.Fatalf("expected active_context to stay empty, got %q", c.ActiveContext)
	}
}

func TestCreate_seedsDefaultContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ABC_CONFIG_FILE", filepath.Join(dir, "config.yaml"))
	path, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "config.yaml") {
		t.Fatalf("path: got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfigYAML(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.ActiveContext != DefaultContextName {
		t.Fatalf("active_context: got %q", cfg.ActiveContext)
	}
	if _, ok := cfg.Contexts[DefaultContextName]; !ok {
		t.Fatal("missing default context")
	}
}
