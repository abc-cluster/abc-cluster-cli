package report

import (
	"encoding/json"
	"io"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// JSONOutput is the top-level shape of `abc report --json`. Stable
// contract per spec abc-report.md §C and §D'; extensions append optional
// sibling keys without changing existing ones.
type JSONOutput struct {
	Window      JSONWindow            `json:"window"`
	ContextName string                `json:"context_name,omitempty"`
	Metrics     map[string]JSONMetric `json:"metrics"`
	RateCard    JSONRateCard          `json:"rate_card"`
	Groups      []JSONGroup           `json:"groups,omitempty"`
}

// JSONWindow renders the Window struct as ISO-8601 strings.
type JSONWindow struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// JSONMetric is one entry in the metrics map. Keyed by metric ID.
type JSONMetric struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Gloss      string `json:"gloss"`
	Unit       Unit   `json:"unit"`
	Value      any    `json:"value"`
	Computable bool   `json:"computable"`
	Reason     string `json:"reason,omitempty"`
}

// JSONRateValue is one rate-card / grid-intensity entry under
// `rate_card`. Each value carries its own provenance so scripts can
// audit which layer produced the number without grepping the text
// footer. Mirrors the shape `internal/accounting`'s rate-card JSON
// emits, just keyed flat for easier downstream consumption.
type JSONRateValue struct {
	Value    any    `json:"value"`
	Source   string `json:"source"`
	Citation string `json:"citation,omitempty"`
}

// JSONRateCard is the structured rate-card surfaced at the top of the
// JSON output. Spec abc-report.md §D' (BINDING): "the `rate_card`
// block at the JSON top level must list every value with `{value,
// source, citation}` for each — not just the postdoc rate."
type JSONRateCard map[string]JSONRateValue

// JSONGroup is one entry in the top-level "groups" array when
// --by=<axis> is used in JSON mode. The map is keyed by metric ID.
// Spec abc-report.md §"v1 grouping": JSON mode supports grouping; text
// mode defers to v2.
type JSONGroup struct {
	By      string                `json:"by"`  // axis name, e.g. "investigation"
	Key     string                `json:"key"` // group value
	Metrics map[string]JSONMetric `json:"metrics"`
}

// RenderJSON encodes the result set as the JSONOutput contract above
// and writes it to w. The caller supplies the window, context, and the
// resolved rate card; results carry the metric values.
func RenderJSON(w io.Writer, window Window, contextName string, card acct.RateCard, results []MetricResult, groups []JSONGroup) error {
	out := JSONOutput{
		Window: JSONWindow{
			Since: window.Since.UTC().Format(time.RFC3339),
			Until: window.Until.UTC().Format(time.RFC3339),
		},
		ContextName: contextName,
		Metrics:     toJSONMetrics(results),
		RateCard:    rateCardToJSON(card),
		Groups:      groups,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func toJSONMetrics(results []MetricResult) map[string]JSONMetric {
	out := make(map[string]JSONMetric, len(results))
	for _, r := range results {
		label := MetricByID(r.ID)
		out[r.ID] = JSONMetric{
			ID:         r.ID,
			Label:      label.Title,
			Gloss:      label.Gloss,
			Unit:       label.Unit,
			Value:      r.Value,
			Computable: r.Computable,
			Reason:     r.Reason,
		}
	}
	return out
}

// rateCardToJSON flattens the resolved RateCard into the JSONRateCard
// shape. Every numeric rate AND grid-intensity value used in the report
// appears as its own entry with provenance, so the "drift between
// verbs is a regression" guarantee from §D' is auditable from a JSON
// payload alone.
func rateCardToJSON(card acct.RateCard) JSONRateCard {
	if card.Currency.Value == "" {
		// Caller passed a zero card; fall back to defaults so the
		// JSON contract never carries empty source/citation fields.
		card = acct.ZADefaults()
	}
	rv := func(v acct.RateValue) JSONRateValue {
		return JSONRateValue{Value: v.Value, Source: string(v.Source), Citation: v.Citation}
	}
	rs := func(v acct.RateString) JSONRateValue {
		return JSONRateValue{Value: v.Value, Source: string(v.Source), Citation: v.Citation}
	}
	return JSONRateCard{
		"currency":                          rs(card.Currency),
		"cost.cpu_hour":                     rv(card.Cost.CpuHour),
		"cost.gpu_hour":                     rv(card.Cost.GpuHour),
		"cost.memory_gb_hour":               rv(card.Cost.MemoryGbHour),
		"cost.storage_scratch_gb_hour":      rv(card.Cost.StorageScratchGbHour),
		"cost.postdoc_per_hour":             rv(card.Cost.PostdocPerHour),
		"emissions.grid_factor_gco2_per_kwh": rv(card.Emissions.GridFactorGco2PerKwh),
		"emissions.cpu_w":                   rv(card.Emissions.CpuW),
		"emissions.gpu_w":                   rv(card.Emissions.GpuW),
		"emissions.memory_gb_w":             rv(card.Emissions.MemoryGbW),
		"emissions.pue":                     rv(card.Emissions.Pue),
		"emissions.storage_scratch_w_per_tb": rv(card.Emissions.StorageScratchWPerTb),
	}
}
