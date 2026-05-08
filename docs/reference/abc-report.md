# `abc report`

Researcher-productivity summary read entirely from `~/.abc/local.db`. No
network calls — `abc report` is a seed-tier-native verb. Cross-institution
aggregation, opt-in sharing, and dashboards are abc-cloud territory and gate
behind the `abc-controller-svc` capability (see `--all-contexts` below).

## Usage

```
abc report                      # personal YTD summary, default text mode
abc report --since=2026-01-01 --until=2026-04-30
abc report --json               # machine-readable; metric IDs as keys
abc report --json --by=investigation
abc report --technical          # metric IDs replace human Titles
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--since=YYYY-MM-DD` | Jan 1 of current year | Window start (UTC) |
| `--until=YYYY-MM-DD` | now | Window end (inclusive end-of-day) |
| `--by=<axis>` | _unset_ | Aggregation axis: `investigation` / `project` / `pipeline` / `user`. **JSON-only in v1**; text mode rejects with a "deferred to v2" message. |
| `--json` | `false` | Emit the structured JSON contract documented below |
| `--technical` | `false` | Replace human Titles with metric IDs in the text headline (useful for reproducible doc snippets) |
| `--all-contexts` | `false` | Cross-context aggregation. **Phase 2**: rejects with `--all-contexts requires abc-controller-svc; not available in this context.` |

## Two-layer metric naming

Every metric has a stable technical ID (frozen at first ship; used in JSON
keys, `--by` flag values, SQL view names, and manuscript scripts) plus a
revisable user-facing Title and Gloss (revisable without an ADR).

| `ID` | `Title` | `Gloss` |
|---|---|---|
| `mttfs` | First-success time | How long from first attempt to first working run |
| `failure_to_result_ratio` | Failed runs per result | How many tries it took to get a usable answer |
| `retry_depth` | Tries before success | Attempts before the run worked |
| `mttr_failure` | Recovery time | How fast you got back on track after a failure |
| `stabilisation_runs` | Runs to settle | Submissions before the pipeline ran reliably |
| `queue_wait_fraction` | Waiting in queue | Share of total time spent waiting, not running |
| `active_engagement_hours` | Hands-on time | Hours actually at the terminal |
| `spectator_hours` | Watching time | Hours spent monitoring runs |
| `async_job_fraction` | Walked-away runs | Runs you submitted and didn't babysit |
| `cognitive_overhead_score` | Tools per workflow | Distinct commands touched to get one result |
| `workflows_unattended` | Hands-off completions | Pipelines that finished without you stepping in |
| `submission_source` | Submission source | How the run was authored: template / handwritten / rerun |
| `resource_fit` | Right-sized requests | How close requested CPU/RAM matched what was used |
| `cost_per_investigation` | Spend per question | Already in `abc accounting --by=investigation`; surfaced here |
| `hours_saved` | Research time saved | Headline composite: estimated busywork avoided |

Adding a metric requires populating all four fields (ID, Title, Gloss, Unit) in
the same patch. The ID contract is enforced by tests; titles are capped at 32
characters.

## Time-saved heuristics

The `hours_saved` headline is the sum of five compile-time heuristics. Values
are intentionally conservative; runtime tuning lands when at least one user
asks.

| Constant | Minutes saved per applicable run | Source |
|---|---|---|
| `AutoRetrySavedMinutes` | 15 | brainstorm §5.10; manual re-submit round-trip |
| `SmartDefaultSavedMinutes` | 10 | resource_fit metric rationale |
| `FailureSummarySavedMinutes` | 30 | structured failure summary vs. log diving |
| `TemplateReuseSavedMinutes` | 60 | template / rerun vs. setup from scratch |
| `AsyncRunSpectatorAvoidedMin` | 30 | spectator_hours; median observed monitoring session |

## Sample output

```
$ abc report
Your 2026 so far:
────────────────────────────────────────────────────
Questions explored (investigations):  3
Pipeline runs:                        12  (10 worked, 2 retried)
Total compute:                        47 CPU-hrs, 0 GPU-hrs

Research time saved (estimated):
  Auto-retry handled it for you      →  ~0.5 hrs
  Smart resource defaults accepted   →  n/a (requires migration 0009 data)
  Failure summaries (vs. log diving) →  ~1.0 hrs
  Reused protocols (vs. from scratch)→  ~2.0 hrs
  ──────────────────────────────────────────────────
  Total:                                ~3.5 hrs

Research time saved:    3.5 hours
Hourly compensation:    R 350
Amount:                 R 1,225

Rate card: Layer-0 ZA defaults (postdoc R350/hr, citation: HSRC 2025).
```

## JSON schema

```json
{
  "window":       {"since": "<RFC3339>", "until": "<RFC3339>"},
  "context_name": "abc-dev",
  "metrics": {
    "<metric-id>": {
      "id":         "<metric-id>",
      "label":      "<human Title>",
      "gloss":      "<one-line gloss>",
      "unit":       "hours|count|percent|currency",
      "value":      <number | object>,
      "computable": true,
      "reason":     "(present only when computable=false)"
    }
  },
  "rate_card": {
    "hourly_rate_zar": 350,
    "citation":        "HSRC 2025",
    "source":          "Layer-0 ZA defaults"
  },
  "groups": [          // present only when --by=<axis> set
    {
      "by":      "investigation",
      "key":     "inv-abc",
      "metrics": { "<metric-id>": { ... } }
    }
  ]
}
```

## Required substrate (migrations)

`abc report` reads existing columns where available and gracefully degrades to
"n/a (reason)" otherwise.

| Migration | Adds | Unlocks metric |
|---|---|---|
| `0008_runs_queue_wait` | `pending_seconds` | `queue_wait_fraction` |
| `0009_runs_resource_request` | `cpu_request`, `mem_request_gb` | `resource_fit` |
| `0010_runs_submission_source` | `submission_source` | `submission_source`, contributes to `hours_saved` |

Run `abc localdb migrate` to apply pending migrations. `abc localdb status`
lists feature flags advertised through the capability layer.

## No network guarantee

`abc report` declares `Required{ AllOf: [{Service: "local-state"}] }` — the
local-state pseudo-service derived from the migration framework. The verb
makes zero outbound HTTP calls in any code path. A test in
`cmd/report/report_test.go` swaps `http.DefaultTransport` for a tripwire and
asserts zero `RoundTrip` calls during render.

## Capability layer

| Need | Backend | Phase |
|---|---|---|
| AllOf `local-state` | local SQLite | seed (always) |
| `--all-contexts` → `abc-controller-svc` (`federation-aggregate`) | controller service | abc-cloud |

`--all-contexts` rejects with the standard capability.Require failure message
when the controller service isn't deployed in the active context's capabilities
map.

## See also

- [`abc accounting`](./abc-accounting) — spend per investigation / project /
  user; same Layer-0 ZA rate card, same provenance footer shape.
- [`abc emissions`](./abc-emissions) — kg CO2e per run.
- [`abc localdb status`](./local-state) — schema version, applied migrations,
  feature flags.
