package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveYAMLVersionIsFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	c := &Config{
		Version:       "1",
		ActiveContext: "dev",
		Contexts: map[string]Context{
			"dev": {Endpoint: "https://api.example.com", AccessToken: "tok"},
		},
	}
	if err := c.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if c.Version != CurrentVersion {
		t.Fatalf("version normalized in memory: got %q want %q", c.Version, CurrentVersion)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	first := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if !strings.HasPrefix(first, "version:") {
		t.Fatalf("first line should be version, got %q", first)
	}
	if !strings.Contains(first, "1.0") {
		t.Fatalf("first line should include 1.0, got %q", first)
	}
	// active_context must appear before contexts (fixed top-level order)
	idxVer := strings.Index(s, "version:")
	idxAC := strings.Index(s, "active_context:")
	idxCtx := strings.Index(s, "contexts:")
	if idxVer < 0 || idxAC < 0 || idxCtx < 0 {
		t.Fatalf("missing keys: ver=%d ac=%d ctx=%d", idxVer, idxAC, idxCtx)
	}
	if !(idxVer < idxAC && idxAC < idxCtx) {
		t.Fatalf("want version < active_context < contexts in file order")
	}
}
