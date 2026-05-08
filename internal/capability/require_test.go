package capability

import (
	"strings"
	"testing"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Sample verb declaration the tests run against. Mirrors the example
// from the brainstorm's "Verb-side patterns" section.
var sampleAccountingReportCapabilities = Required{
	AnyOf: []Need{
		{Service: "abc-accounting-svc", MinVersion: "0.4.0"},
		{Service: "local-state", MinVersion: "0001_initial"},
	},
	OptionalFor: map[string]Need{
		"signed":          {Service: "abc-fleet-svc", MinVersion: "0.5.0"},
		"include-storage": {Service: "local-state", MinVersion: "0004_add_scratch_gb",
			Features: []string{"scratch-storage-attribution"}},
	},
}

func TestRequire_PreferredBackend(t *testing.T) {
	// Grove tier with both backends available — should pick the preferred
	// (first listed) abc-accounting-svc.
	caps := MockMap(TierGroveTended).
		AtVersion("abc-accounting-svc", "0.5.0").
		Build()

	d := Require(sampleAccountingReportCapabilities, caps, nil)
	if d.Failed() {
		t.Fatalf("expected pass; got %+v", d)
	}
	if d.Backend != "abc-accounting-svc" {
		t.Errorf("expected abc-accounting-svc, got %q", d.Backend)
	}
	if d.Degraded {
		t.Errorf("expected non-degraded; got banner %q", d.Banner)
	}
}

func TestRequire_DegradedToLocalState(t *testing.T) {
	// Grove tier with the preferred backend missing — should fall to
	// local-state with Degraded=true and a banner.
	caps := MockMap(TierGrove).
		Without("abc-accounting-svc").
		Build()

	d := Require(sampleAccountingReportCapabilities, caps, nil)
	if d.Failed() {
		t.Fatalf("expected pass with degradation; got %+v", d)
	}
	if d.Backend != "local-state" {
		t.Errorf("expected local-state backend, got %q", d.Backend)
	}
	if !d.Degraded {
		t.Error("expected Degraded=true")
	}
	if !strings.Contains(d.Banner, "abc-accounting-svc") {
		t.Errorf("expected banner to surface tech name; got %q", d.Banner)
	}
	// Per the 2026-05-08 UX decision: CLI output uses tech names only.
	// The banner must NOT include the codename ("Kayastha").
	if strings.Contains(d.Banner, "Kayastha") {
		t.Errorf("banner must not include the codename; got %q", d.Banner)
	}
}

func TestRequire_AllBackendsMissing_Failed(t *testing.T) {
	// AnyOf with neither option present → fail.
	caps := MockMap(TierSeedling).Without("local-state").Build()

	d := Require(sampleAccountingReportCapabilities, caps, nil)
	if !d.Failed() {
		t.Fatalf("expected failure; got %+v", d)
	}
	if d.FailReason == "" {
		t.Error("expected a non-empty FailReason")
	}
}

func TestRequire_OptionalFlagBlocked(t *testing.T) {
	// User passes --signed but Veld isn't deployed → flag must be
	// blocked (not silently dropped). Verb sees UnusableFlags and
	// fails with a clear error.
	caps := MockMap(TierGrove).
		AtVersion("abc-accounting-svc", "0.5.0").
		Build()

	d := Require(sampleAccountingReportCapabilities, caps, map[string]bool{"signed": true})
	if !d.Failed() {
		t.Fatalf("expected failure due to --signed without Veld; got %+v", d)
	}
	if len(d.UnusableFlags) != 1 {
		t.Fatalf("expected 1 unusable flag; got %d", len(d.UnusableFlags))
	}
	if d.UnusableFlags[0].Flag != "signed" {
		t.Errorf("expected --signed blocked; got %q", d.UnusableFlags[0].Flag)
	}
	err := d.AsError()
	if err == nil || !strings.Contains(err.Error(), "abc-fleet-svc") {
		t.Errorf("error should surface tech name; got %v", err)
	}
	// Per the 2026-05-08 UX decision: CLI output uses tech names only.
	if err != nil && strings.Contains(err.Error(), "Veld") {
		t.Errorf("error must not include the codename; got %v", err)
	}
}

func TestRequire_OptionalFlagAccepted(t *testing.T) {
	// User passes --signed AND Veld is deployed at the right version → pass.
	caps := MockMap(TierGroveTended).
		AtVersion("abc-accounting-svc", "0.5.0").
		AtVersion("abc-fleet-svc", "0.5.0").
		Build()

	d := Require(sampleAccountingReportCapabilities, caps, map[string]bool{"signed": true})
	if d.Failed() {
		t.Fatalf("expected pass; got %+v", d)
	}
	if len(d.UnusableFlags) != 0 {
		t.Errorf("expected no unusable flags; got %v", d.UnusableFlags)
	}
}

func TestRequire_AllOf(t *testing.T) {
	// AllOf with both needs present → pass.
	decl := Required{
		AllOf: []Need{
			{Service: "abc-bitemporal-svc", MinVersion: "0.4.0",
				Features: []string{"lucene-search"}},
		},
	}
	caps := MockMap(TierGrove).
		AtVersion("abc-bitemporal-svc", "0.4.2").
		WithFeatures("abc-bitemporal-svc", "lucene-search").
		Build()
	if d := Require(decl, caps, nil); d.Failed() {
		t.Errorf("expected pass; got %+v", d)
	}

	// AllOf with feature missing → fail.
	caps2 := MockMap(TierGrove).
		AtVersion("abc-bitemporal-svc", "0.4.2").
		Build() // no features set
	d := Require(decl, caps2, nil)
	if !d.Failed() {
		t.Errorf("expected failure when feature missing; got pass")
	}
	if !strings.Contains(d.FailReason, "lucene-search") {
		t.Errorf("expected FailReason to mention missing feature; got %q", d.FailReason)
	}
}

func TestFormatService(t *testing.T) {
	// Per the 2026-05-08 UX decision: CLI output uses tech names only.
	// FormatService is a no-op pass-through; it does not append the
	// codename. Codenames live in docs and glossary, not in CLI output.
	tests := []struct{ tech, want string }{
		{"abc-fleet-svc", "abc-fleet-svc"},
		{"abc-accounting-svc", "abc-accounting-svc"},
		{"abc-data-api", "abc-data-api"},
		{"local-state", "local-state"},
		{"abc-controller-svc", "abc-controller-svc"},
	}
	for _, tt := range tests {
		got := FormatService(tt.tech)
		if got != tt.want {
			t.Errorf("FormatService(%q) = %q; want %q", tt.tech, got, tt.want)
		}
	}
}

func TestFresh(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		caps     *cfg.Capabilities
		expected Freshness
	}{
		{"nil → first run", nil, FirstRun},
		{"zero LastSynced → first run", &cfg.Capabilities{}, FirstRun},
		{"5 min ago → fresh", &cfg.Capabilities{LastSynced: now.Add(-5 * time.Minute)}, FreshCache},
		{"1 hr ago → revalidate", &cfg.Capabilities{LastSynced: now.Add(-1 * time.Hour)}, RevalidateInBg},
		{"2 days ago → blocking", &cfg.Capabilities{LastSynced: now.Add(-2 * 24 * time.Hour)}, BlockingProbe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Fresh(tt.caps); got != tt.expected {
				t.Errorf("Fresh = %v; want %v", got, tt.expected)
			}
		})
	}
}
