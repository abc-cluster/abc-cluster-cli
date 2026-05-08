// Package report computes researcher-productivity metrics from
// ~/.abc/local.db and renders them as text or JSON. Seed-tier-native:
// no network calls, no controller dependency. Spec abc-report.md.
//
// The package exposes the v1 metric taxonomy (15 entries — see
// MetricLabel and the Metrics map below), per-metric query functions
// (queries.go), the time-saved heuristic constants (heuristics.go), and
// two renderers (render_text.go, render_json.go).
//
// Naming convention (BINDING, per spec §"Naming convention"):
//
//   - ID is snake_case and frozen at ship; used as JSON keys, --by flag
//     values, view names, ADR references, manuscript scripts.
//   - Title is the plain-language label rendered in default text output;
//     revisable based on user feedback without an ADR.
//   - Gloss is the one-line explanation rendered beneath the value.
//   - Unit is one of "hours" / "count" / "percent" / "currency".
//
// A new metric MUST land with all four fields populated in the same
// patch. The unit tests in labels_test.go enforce non-empty fields, the
// ID regex, and the < 32-char title length.
package report

import "regexp"

// Unit is the rendering unit for a metric value.
type Unit string

const (
	UnitHours    Unit = "hours"
	UnitCount    Unit = "count"
	UnitPercent  Unit = "percent"
	UnitCurrency Unit = "currency"
)

// MetricLabel is the immutable description of a metric. ID and Unit are
// frozen at first ship; Title and Gloss are revisable without an ADR.
type MetricLabel struct {
	ID    string
	Title string
	Gloss string
	Unit  Unit
}

// IDPattern is the regex IDs must match. Locked by the labels_test.go
// guard so a typo in a future metric addition fails CI rather than
// shipping a broken ID into a JSON contract.
var IDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// MaxTitleLen caps the user-facing label length so the default text
// output stays readable in an 80-column terminal.
const MaxTitleLen = 32

// Metrics is the v1 metric mapping. Locked by spec abc-report.md
// §"Naming convention". Order here is the order metrics appear in the
// default --json output.
//
// To add a metric: append here, populate all four fields, add the
// corresponding query function in queries.go, update render_text.go if
// the new metric belongs in the default summary block, and bump the
// migration if new substrate is required.
var Metrics = []MetricLabel{
	{ID: "mttfs", Title: "First-success time", Gloss: "How long from first attempt to first working run", Unit: UnitHours},
	{ID: "failure_to_result_ratio", Title: "Failed runs per result", Gloss: "How many tries it took to get a usable answer", Unit: UnitCount},
	{ID: "retry_depth", Title: "Tries before success", Gloss: "Attempts before the run worked", Unit: UnitCount},
	{ID: "mttr_failure", Title: "Recovery time", Gloss: "How fast you got back on track after a failure", Unit: UnitHours},
	{ID: "stabilisation_runs", Title: "Runs to settle", Gloss: "Submissions before the pipeline ran reliably", Unit: UnitCount},
	{ID: "queue_wait_fraction", Title: "Waiting in queue", Gloss: "Share of total time spent waiting, not running", Unit: UnitPercent},
	{ID: "active_engagement_hours", Title: "Hands-on time", Gloss: "Hours actually at the terminal", Unit: UnitHours},
	{ID: "spectator_hours", Title: "Watching time", Gloss: "Hours spent monitoring runs", Unit: UnitHours},
	{ID: "async_job_fraction", Title: "Walked-away runs", Gloss: "Runs you submitted and didn't babysit", Unit: UnitPercent},
	{ID: "cognitive_overhead_score", Title: "Tools per workflow", Gloss: "Distinct commands touched to get one result", Unit: UnitCount},
	{ID: "workflows_unattended", Title: "Hands-off completions", Gloss: "Pipelines that finished without you stepping in", Unit: UnitCount},
	{ID: "submission_source", Title: "Submission source", Gloss: "How the run was authored: template / handwritten / rerun", Unit: UnitCount},
	{ID: "resource_fit", Title: "Right-sized requests", Gloss: "How close requested CPU/RAM matched what was used", Unit: UnitPercent},
	{ID: "cost_per_investigation", Title: "Spend per question", Gloss: "Already in `abc accounting --by=investigation`; surfaced here", Unit: UnitCurrency},
	{ID: "hours_saved", Title: "Research time saved", Gloss: "Headline composite: estimated busywork avoided", Unit: UnitHours},
}

// MetricByID returns the label for an ID, or zero MetricLabel if
// unknown. Used by renderers when they only carry the ID.
func MetricByID(id string) MetricLabel {
	for _, m := range Metrics {
		if m.ID == id {
			return m
		}
	}
	return MetricLabel{}
}
