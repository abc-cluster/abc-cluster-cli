package report

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// TestRenderText_AcceptanceShape: the §"Acceptance: end-to-end" sample
// in the spec gives the canonical text-mode shape. We don't pin every
// number, but we do pin the section headers and the rate-card footer
// — drift in those is the kind of breakage downstream doc snippets
// catch only after they've been regenerated wrong.
func TestRenderText_AcceptanceShape(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderText(context.Background(), db, &buf, TextOptions{Window: fullWindow}, res); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Questions explored (investigations):",
		"Pipeline runs:",
		"Total compute:",
		"Research time saved (estimated):",
		"Auto-retry handled it for you",
		"Failure summaries (vs. log diving)",
		"Reused protocols (vs. from scratch)",
		"Spend this period:",
		"Emissions this period:",
		"Research time saved:",
		"Hourly compensation:",
		"Amount:",
		"Rate card (effective):",
		"cost.postdoc_per_hour",
		"emissions.grid_factor_gco2_per_kwh",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderText output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestRenderText_TechnicalMode: --technical replaces the human Title
// labels with the metric IDs in the headline lines (still keeps the
// gloss text where rendered). Useful sanity check for doc snippets.
func TestRenderText_TechnicalMode(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)
	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderText(context.Background(), db, &buf, TextOptions{Window: fullWindow, Technical: true}, res); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	for _, id := range []string{"investigations_count", "runs_count", "compute_hours", "hours_saved", "postdoc_per_hour", "amount_zar", "spend_zar", "emissions_kgco2e"} {
		if !strings.Contains(out, id) {
			t.Errorf("--technical text missing %q", id)
		}
	}
}

// TestRenderJSON_HoursSavedShape: the JSON payload includes a
// `metrics.hours_saved` entry with the four required sibling keys
// (label, gloss, unit, value, computable). Spec §"Acceptance" sample.
func TestRenderJSON_HoursSavedShape(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)
	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderJSON(&buf, fullWindow, "ctx", acct.ZADefaults(), res, nil); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	var top map[string]any
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("unmarshal: %v\noutput:\n%s", err, buf.String())
	}
	metrics, ok := top["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("missing metrics map; got %T", top["metrics"])
	}
	hs, ok := metrics["hours_saved"].(map[string]any)
	if !ok {
		t.Fatalf("missing hours_saved entry")
	}
	for _, k := range []string{"id", "label", "gloss", "unit", "value", "computable"} {
		if _, ok := hs[k]; !ok {
			t.Errorf("hours_saved missing key %q", k)
		}
	}
	if hs["label"] != "Research time saved" {
		t.Errorf("hours_saved.label = %v, want Research time saved", hs["label"])
	}
	if hs["unit"] != "hours" {
		t.Errorf("hours_saved.unit = %v, want hours", hs["unit"])
	}
}

// TestRenderJSON_RateCardFooter: the JSON output includes a structured
// rate_card object enumerating every rate value with its provenance.
// Spec §D' (BINDING): "the `rate_card` block at the JSON top level
// must list every value with `{value, source, citation}` for each —
// not just the postdoc rate."
func TestRenderJSON_RateCardFooter(t *testing.T) {
	db := openFixtureDB(t)
	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderJSON(&buf, fullWindow, "ctx", acct.ZADefaults(), res, nil); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	rc, ok := top["rate_card"].(map[string]any)
	if !ok {
		t.Fatalf("missing rate_card; got %T", top["rate_card"])
	}
	// Every required key present with {value, source, citation}.
	for _, key := range []string{
		"currency", "cost.cpu_hour", "cost.gpu_hour", "cost.memory_gb_hour",
		"cost.storage_scratch_gb_hour", "cost.postdoc_per_hour",
		"emissions.grid_factor_gco2_per_kwh", "emissions.cpu_w", "emissions.gpu_w",
		"emissions.memory_gb_w", "emissions.pue", "emissions.storage_scratch_w_per_tb",
	} {
		entry, ok := rc[key].(map[string]any)
		if !ok {
			t.Errorf("rate_card[%q] missing or wrong type: %T", key, rc[key])
			continue
		}
		if _, ok := entry["value"]; !ok {
			t.Errorf("rate_card[%q].value missing", key)
		}
		if _, ok := entry["source"]; !ok {
			t.Errorf("rate_card[%q].source missing", key)
		}
	}
	// Postdoc rate specifically: must be R350 from Layer 0 ZA defaults.
	pd, _ := rc["cost.postdoc_per_hour"].(map[string]any)
	if v, _ := pd["value"].(float64); v != acct.ZADefaultCostPostdocPerHour {
		t.Errorf("cost.postdoc_per_hour.value = %v, want %v", pd["value"], acct.ZADefaultCostPostdocPerHour)
	}
}

// TestDefaultWindowYTD: asserts the YTD anchor is Jan 1 UTC and the
// upper bound is the supplied `now`.
func TestDefaultWindowYTD(t *testing.T) {
	now := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)
	w := DefaultWindowYTD(now)
	if w.Since.Year() != 2026 || w.Since.Month() != time.January || w.Since.Day() != 1 {
		t.Errorf("Since = %v, want 2026-01-01", w.Since)
	}
	if !w.Until.Equal(now) {
		t.Errorf("Until = %v, want %v", w.Until, now)
	}
}

// TestFormatThousands: spot-checks the comma formatter the renderer uses
// for the rand amount line.
func TestFormatThousands(t *testing.T) {
	cases := map[float64]string{
		0:        "0",
		999:      "999",
		1000:     "1,000",
		1225:     "1,225",
		1234567:  "1,234,567",
	}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%v) = %q, want %q", in, got, want)
		}
	}
}
