package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// today returns today's date in the format used by absorbConfigs.
func today() string {
	return time.Now().Format("20060102")
}

// makeConfig builds a minimal *cfg.Config with the given primary context names.
func makeConfig(names ...string) *cfg.Config {
	c := &cfg.Config{
		Version:        cfg.CurrentVersion,
		Contexts:       map[string]cfg.Context{},
		ContextAliases: map[string]string{},
	}
	for _, n := range names {
		c.Contexts[n] = cfg.Context{Endpoint: "https://example.com"}
	}
	return c
}

// TestAbsorbFreshContext verifies that a brand-new context name is added as-is.
func TestAbsorbFreshContext(t *testing.T) {
	dest := makeConfig("existing")
	incoming := makeConfig("brand-new")

	entries := absorbConfigs(dest, incoming)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].original != "brand-new" {
		t.Errorf("original = %q, want %q", entries[0].original, "brand-new")
	}
	if entries[0].final != "brand-new" {
		t.Errorf("final = %q, want %q (no rename expected)", entries[0].final, "brand-new")
	}
	if _, ok := dest.Contexts["brand-new"]; !ok {
		t.Error("dest.Contexts[\"brand-new\"] not found after absorb")
	}
}

// TestAbsorbCollision verifies that a colliding name is renamed to <name>-1-<date>.
func TestAbsorbCollision(t *testing.T) {
	dest := makeConfig("alpha")
	incoming := makeConfig("alpha")

	entries := absorbConfigs(dest, incoming)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	want := fmt.Sprintf("alpha-1-%s", today())
	if entries[0].final != want {
		t.Errorf("final = %q, want %q", entries[0].final, want)
	}
	if _, ok := dest.Contexts[want]; !ok {
		t.Errorf("dest.Contexts[%q] not found", want)
	}
	// Original "alpha" must still exist.
	if _, ok := dest.Contexts["alpha"]; !ok {
		t.Error("original alpha context was removed")
	}
}

// TestAbsorbCollisionIncrement verifies that when <name>-1-<date> also exists the
// counter increments to 2.
func TestAbsorbCollisionIncrement(t *testing.T) {
	name1 := fmt.Sprintf("alpha-1-%s", today())
	dest := makeConfig("alpha", name1)
	incoming := makeConfig("alpha")

	entries := absorbConfigs(dest, incoming)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	want := fmt.Sprintf("alpha-2-%s", today())
	if entries[0].final != want {
		t.Errorf("final = %q, want %q", entries[0].final, want)
	}
	if _, ok := dest.Contexts[want]; !ok {
		t.Errorf("dest.Contexts[%q] not found", want)
	}
}

// TestAbsorbDoesNotChangeActiveContext verifies active_context is untouched.
func TestAbsorbDoesNotChangeActiveContext(t *testing.T) {
	dest := makeConfig("existing")
	dest.ActiveContext = "existing"
	incoming := makeConfig("newctx")

	absorbConfigs(dest, incoming)

	if dest.ActiveContext != "existing" {
		t.Errorf("active_context changed to %q, want %q", dest.ActiveContext, "existing")
	}
}

// TestAbsorbInvalidIncomingFile verifies that an incoming file with an invalid
// cluster_type causes an error before the merge happens.
func TestAbsorbInvalidIncomingFile(t *testing.T) {
	// Write a temp YAML file with a bad cluster_type.
	dir := t.TempDir()
	badYAML := `version: "1.0"
contexts:
  bad-ctx:
    endpoint: "https://example.com"
    cluster_type: "not-a-valid-type"
`
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(badYAML), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	incoming, err := cfg.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom unexpected error: %v", err)
	}
	if err := incoming.Validate(); err == nil {
		t.Fatal("expected Validate() to return an error for invalid cluster_type, got nil")
	}
}
