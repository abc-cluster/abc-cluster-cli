package pipeline

import (
	"strings"
	"testing"
)

func TestValidatePluginVersionConsistency(t *testing.T) {
	cases := []struct {
		name    string
		plugins []PluginRef
		wantErr bool
	}{
		{
			name:    "empty set is fine",
			plugins: nil,
		},
		{
			name: "all bare is fine (each resolves to newest published)",
			plugins: []PluginRef{
				{ID: "nf-nomad"},
				{ID: "nf-nomad-s5cmd"},
			},
		},
		{
			name: "all pinned is fine (explicit customization)",
			plugins: []PluginRef{
				{ID: "nf-nomad", Version: "0.4.0-edge8"},
				{ID: "nf-nomad-s5cmd", Version: "0.1.4"},
			},
		},
		{
			name: "dev set (all 99.99.99) is fine",
			plugins: []PluginRef{
				{ID: "nf-nomad", Version: "99.99.99"},
				{ID: "nf-nomad-s5cmd", Version: "99.99.99"},
			},
		},
		{
			// The footgun: --plugin nf-nomad-s5cmd@0.1.4 leaves nf-nomad bare.
			name: "mixed pinned + bare is rejected",
			plugins: []PluginRef{
				{ID: "nf-nomad-s5cmd", Version: "0.1.4"},
				{ID: "nf-nomad"},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginVersionConsistency(tc.plugins)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error for %q, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got: %v", tc.name, err)
			}
		})
	}
}

// The error must name both the pinned and the bare plugin, and offer both
// resolutions, so the user can fix it without guessing.
func TestValidatePluginVersionConsistency_ActionableMessage(t *testing.T) {
	err := validatePluginVersionConsistency([]PluginRef{
		{ID: "nf-nomad-s5cmd", Version: "0.1.4"},
		{ID: "nf-nomad"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"nf-nomad-s5cmd@0.1.4", "nf-nomad", "--plugin", "pin none", "Unknown Nextflow plugin"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got:\n%s", want, msg)
		}
	}
}
