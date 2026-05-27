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

// TestRenderText_AcceptanceShape: pins the default-render section
// headers + the always-on disclaimer. Updated 2026-05-27 — the
// "Research time saved" block and the detailed "Rate card (effective)"
// block both moved off the default path (the former removed pending
// real measurement, the latter gated behind --show-rate-card). See
// TestRenderText_ShowRateCard for the gated-on path.
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
		"Spend this period:",
		"Emissions this period:",
		"These rates are suggestive based on the reasonable default values,",
		"the real-time rates are coming soon.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderText output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Things that must NOT appear in default output (the surface
	// changes that motivated this rev; regression-pin them).
	for _, mustNotHave := range []string{
		"Research time saved",   // removed pending real instrumentation
		"Auto-retry handled",    // sub-row of removed block
		"Hourly compensation:",  // currency translation of removed block
		"Rate card (effective):", // gated behind --show-rate-card
		"cost.postdoc_per_hour", // ditto
		"showback estimates; not invoice-grade", // old disclaimer wording
	} {
		if strings.Contains(out, mustNotHave) {
			t.Errorf("default render leaked %q (should be gated or removed)\nfull output:\n%s", mustNotHave, out)
		}
	}
}

// TestRenderText_ShowRateCard: --show-rate-card surfaces the full
// per-rate provenance footer + override hints. The same disclaimer
// wording is shared with the default path so attendees see one
// consistent line.
func TestRenderText_ShowRateCard(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)

	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderText(context.Background(), db, &buf, TextOptions{Window: fullWindow, ShowRateCard: true}, res); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Rate card (effective):",
		"cost.postdoc_per_hour",
		"emissions.grid_factor_gco2_per_kwh",
		"suggestive based on the reasonable default values",
		"abc config accounting set cost.postdoc_per_hour=400",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--show-rate-card output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestRenderText_TechnicalMode: --technical replaces the human Title
// labels with the metric IDs in the headline lines (still keeps the
// gloss text where rendered). Updated 2026-05-27: the metric IDs
// asserted are limited to the metrics still in the default render
// (the hours_saved family moved off the default path; see
// TestRenderText_AcceptanceShape).
func TestRenderText_TechnicalMode(t *testing.T) {
	db := openFixtureDB(t)
	insertRun(t, db, "r1", "completed", 1700000000, 1700003600, 3600, 1.0)
	res := Compute(context.Background(), db, QueryOptions{Window: fullWindow, ContextName: "ctx"})
	var buf bytes.Buffer
	if err := RenderText(context.Background(), db, &buf, TextOptions{Window: fullWindow, Technical: true}, res); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	for _, id := range []string{"investigations_count", "runs_count", "compute_hours", "spend_zar", "emissions_kgco2e"} {
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
