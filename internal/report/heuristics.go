package report

// Time-saved heuristic constants used to compute the headline
// `hours_saved` metric. Spec abc-report.md §C: each constant carries a
// citation back to the brainstorm or supporting literature, and values
// are intentionally conservative.
//
// These constants are compile-time in v1; per spec §"Out of scope",
// runtime tuning (e.g. `--auto-retry-saved-minutes=20`) is deferred
// until at least one user asks for it.
//
// To revise a value:
//   1. Update the constant + the citation comment.
//   2. Bump the abc-report version note in docs/reference/abc-report.md.
//   3. NO ADR required (heuristics are tunable v1 inputs); but if the
//      change inverts a sign or flips a category, write one.
const (
	// AutoRetrySavedMinutes is the time the abc retry layer saves vs.
	// the user manually re-submitting after a transient failure.
	// Source: brainstorm §5.10 sample output; assumes one auto-retry
	// per transient failure and a 15-min user round-trip (notice ->
	// re-edit -> re-submit -> watch).
	AutoRetrySavedMinutes = 15

	// SmartDefaultSavedMinutes is the time saved per run when the
	// platform's resource defaults are accepted unchanged vs. the user
	// hand-tuning CPU / memory / scratch. Source: resource_fit metric
	// rationale in brainstorm §5.4.
	SmartDefaultSavedMinutes = 10

	// FailureSummarySavedMinutes is the time saved per failed run by
	// the structured failure summary surfaced via `abc job status`
	// vs. tail-following alloc logs. Source: brainstorm §5.10
	// ("vs. log diving"); 30 min is the conservative end of the
	// log-diving range commonly cited in DevOps surveys.
	FailureSummarySavedMinutes = 30

	// TemplateReuseSavedMinutes is the time saved per run authored
	// from a template (submission_source = "template:<id>" or
	// "rerun") vs. handwritten. Source: brainstorm §5.4 reproducibility
	// argument; 60 min covers params/samplesheet/profile setup that a
	// template skips.
	TemplateReuseSavedMinutes = 60

	// AsyncRunSpectatorAvoidedMin is the time saved per async run —
	// runs the user submitted and didn't babysit (no watch-verb
	// cli_audit entries between submit and complete). Source:
	// spectator_hours metric in brainstorm §5.4; 30 min is the
	// median observed monitoring session in informal interviews
	// (placeholder until the diary instrument lands).
	AsyncRunSpectatorAvoidedMin = 30
)
