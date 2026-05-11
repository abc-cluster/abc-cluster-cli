package config

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

func TestContextLookupFor_KeyMapping(t *testing.T) {
	ctx := Context{
		Endpoint:       "https://api.example",
		AccessToken:    "tok",
		WorkspaceID:    "ws",
		Region:         "za",
		Namespace:      "ns",
		OrgID:          "org",
		OutputFormat:   "json",
		UploadEndpoint: "https://up.example",
		UploadToken:    "uptok",
		ControllerURL:  "https://ctrl.example",
		ClusterType:    "abc-grove",
	}
	lookup := ContextLookupFor(ctx)

	cases := map[string]string{
		"url":             "https://api.example",
		"access_token":    "tok",
		"workspace_id":    "ws",
		"region":          "za",
		"namespace":       "ns",
		"org_id":          "org",
		"output_format":   "json",
		"upload_endpoint": "https://up.example",
		"upload_token":    "uptok",
		"controller_url":  "https://ctrl.example",
		"cluster_type":    "abc-grove",
	}
	for key, want := range cases {
		got, ok := lookup(key)
		if !ok || got != want {
			t.Errorf("lookup(%q) = (%q, %v); want (%q, true)", key, got, ok, want)
		}
	}
}

func TestContextLookupFor_EmptyFieldReturnsNotOK(t *testing.T) {
	lookup := ContextLookupFor(Context{Endpoint: "x"})
	_, ok := lookup("access_token")
	if ok {
		t.Error("empty AccessToken should report ok=false (not '' treated as set)")
	}
}

func TestContextLookupFor_UnknownKey(t *testing.T) {
	lookup := ContextLookupFor(Context{Endpoint: "x"})
	_, ok := lookup("does_not_exist")
	if ok {
		t.Error("unknown key should report ok=false")
	}
}

func TestActiveContextLookup_NoActive(t *testing.T) {
	cfg := &Config{}
	got := ActiveContextLookup(cfg)
	// The returned function should behave like envvars.NoContext.
	_, ok := got("url")
	if ok {
		t.Error("expected NoContext-like behaviour when no active context")
	}
	_ = envvars.NoContext // referenced to keep import meaningful
}

func TestActiveContextLookup_WithActive(t *testing.T) {
	cfg := &Config{
		ActiveContext: "primary",
		Contexts: map[string]Context{
			"primary": {Endpoint: "https://primary.example"},
		},
	}
	got := ActiveContextLookup(cfg)
	v, ok := got("url")
	if !ok || v != "https://primary.example" {
		t.Errorf("got (%q, %v); want (https://primary.example, true)", v, ok)
	}
}
