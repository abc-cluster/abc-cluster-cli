package report

import (
	"encoding/json"
	"io"
	"time"
)

// JSONOutput is the top-level shape of `abc report --json`. Stable
// contract per spec abc-report.md §C; extensions append optional
// sibling keys without changing existing ones.
type JSONOutput struct {
	Window       JSONWindow                `json:"window"`
	ContextName  string                    `json:"context_name,omitempty"`
	Metrics      map[string]JSONMetric     `json:"metrics"`
	RateCard     JSONRateCard              `json:"rate_card"`
	Groups       []JSONGroup               `json:"groups,omitempty"`
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

// JSONRateCard surfaces the postdoc-rate provenance the text footer
// shows, as a structured object that scripts can consume.
type JSONRateCard struct {
	HourlyRateZAR int    `json:"hourly_rate_zar"`
	Citation      string `json:"citation"`
	Source        string `json:"source"`
}

// JSONGroup is one entry in the top-level "groups" array when
// --by=<axis> is used in JSON mode. The map is keyed by metric ID.
// Spec abc-report.md §"v1 grouping": JSON mode supports grouping; text
// mode defers to v2.
type JSONGroup struct {
	By      string                `json:"by"`      // axis name, e.g. "investigation"
	Key     string                `json:"key"`     // group value
	Metrics map[string]JSONMetric `json:"metrics"`
}

// RenderJSON encodes the result set as the JSONOutput contract above
// and writes it to w. The caller supplies the window and context for
// the header; results carry the metric values.
func RenderJSON(w io.Writer, window Window, contextName string, results []MetricResult, groups []JSONGroup) error {
	out := JSONOutput{
		Window: JSONWindow{
			Since: window.Since.UTC().Format(time.RFC3339),
			Until: window.Until.UTC().Format(time.RFC3339),
		},
		ContextName: contextName,
		Metrics:     toJSONMetrics(results),
		RateCard: JSONRateCard{
			HourlyRateZAR: HourlyRateZAR,
			Citation:      "HSRC 2025",
			Source:        "Layer-0 ZA defaults",
		},
		Groups: groups,
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
