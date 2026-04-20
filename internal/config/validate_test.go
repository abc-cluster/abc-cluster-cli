package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateActiveContextUnknown(t *testing.T) {
	t.Parallel()
	c := &Config{
		Version:       CurrentVersion,
		ActiveContext: "missing",
		Contexts: map[string]Context{
			"lab": {Endpoint: "https://x", AccessToken: "t"},
		},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown active_context")
	}
}

func TestLoadMigratesBareHTTPNomadAddr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := []byte(`version: "1.0"
active_context: lab
contexts:
  lab:
    endpoint: https://x.example
    access_token: t
    admin:
      services:
        nomad:
          nomad_addr: http://10.0.0.1
          nomad_token: tok
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Contexts["lab"].NomadAddr(); got != "http://10.0.0.1:4646" {
		t.Fatalf("nomad_addr: got %q want canonical with :4646", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateClusterTypeInvalid(t *testing.T) {
	t.Parallel()
	c := &Config{
		Version: CurrentVersion,
		Contexts: map[string]Context{
			"lab": {
				Endpoint:     "https://x",
				AccessToken:  "t",
				ClusterType:  "not-a-tier",
			},
		},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid cluster_type")
	}
}

func TestFmtRoundTripSortedKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Intentionally unsorted context keys and inner keys (YAML map order preserved on parse then re-emitted sorted).
	raw := []byte(`version: "1.0"
active_context: lab
contexts:
  lab:
    access_token: tok
    cluster_type: abc-nodes
    endpoint: https://api.example
    admin:
      services:
        nomad:
          nomad_addr: http://10.0.0.1:4646
          nomad_token: secret
`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	out, err := cfg.MarshalDocumentYAML()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
	cfg2, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := cfg2.MarshalDocumentYAML()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(out2) {
		t.Fatalf("second marshal should be stable:\n%s\nvs\n%s", string(out), string(out2))
	}
}
