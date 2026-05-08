package report

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMetrics_AllFieldsPresent: every entry in Metrics has all four
// fields populated. Spec abc-report.md §C: "every entry has all four
// fields non-empty".
func TestMetrics_AllFieldsPresent(t *testing.T) {
	for _, m := range Metrics {
		if m.ID == "" || m.Title == "" || m.Gloss == "" || m.Unit == "" {
			t.Errorf("metric %q: missing field(s) %+v", m.ID, m)
		}
	}
}

// TestMetrics_IDPattern: every ID matches ^[a-z][a-z0-9_]*$ so it can
// be used as a JSON key, --by flag value, and SQL view name without
// quoting. Frozen contract.
func TestMetrics_IDPattern(t *testing.T) {
	for _, m := range Metrics {
		if !IDPattern.MatchString(m.ID) {
			t.Errorf("metric %q: ID does not match %s", m.ID, IDPattern)
		}
	}
}

// TestMetrics_TitleLength: titles are shown in the default text output
// and must fit under MaxTitleLen runes for an 80-column terminal.
func TestMetrics_TitleLength(t *testing.T) {
	for _, m := range Metrics {
		if utf8.RuneCountInString(m.Title) >= MaxTitleLen {
			t.Errorf("metric %q: title %q is %d runes, want < %d",
				m.ID, m.Title, utf8.RuneCountInString(m.Title), MaxTitleLen)
		}
	}
}

// TestMetrics_UniqueIDs: defends against typo-induced duplicate entries.
func TestMetrics_UniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Metrics {
		if seen[m.ID] {
			t.Errorf("duplicate metric ID: %q", m.ID)
		}
		seen[m.ID] = true
	}
}

// TestMetricByID: hit and miss cases.
func TestMetricByID(t *testing.T) {
	if got := MetricByID("hours_saved"); got.Title != "Research time saved" {
		t.Errorf("MetricByID(hours_saved).Title = %q", got.Title)
	}
	if got := MetricByID("nonexistent"); got.ID != "" {
		t.Errorf("MetricByID(nonexistent) = %+v, want zero", got)
	}
}

// TestMetrics_KnownUnits: only the four whitelisted units may appear.
func TestMetrics_KnownUnits(t *testing.T) {
	allowed := map[Unit]bool{UnitHours: true, UnitCount: true, UnitPercent: true, UnitCurrency: true}
	for _, m := range Metrics {
		if !allowed[m.Unit] {
			t.Errorf("metric %q: unit %q not in {hours,count,percent,currency}", m.ID, m.Unit)
		}
	}
}

// TestMetrics_GlossNoTrailingPunctuation is a soft-style check: glosses
// flow into a single line in the text renderer; trailing punctuation
// looks awkward. Not a freeze constraint, but catches drift early.
func TestMetrics_GlossNoTrailingPunctuation(t *testing.T) {
	for _, m := range Metrics {
		if strings.HasSuffix(m.Gloss, ".") {
			t.Errorf("metric %q: gloss ends with '.', want no trailing period", m.ID)
		}
	}
}
