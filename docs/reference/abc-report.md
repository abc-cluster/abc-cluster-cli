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
| `cost_per_investigation` | Spend per question | ZAR per investigation, resolved via `internal/accounting` rate card |
| `emissions_per_investigation` | Carbon per question | kg CO₂e attributed to each investigation, resolved via `internal/emissions` |
| `spend_zar` | Total spend | Window-wide ZAR aggregate, computed via the shared `internal/accounting` rate-card resolver |
| `emissions_kgco2e` | Total emissions | Window-wide kg CO₂e, computed via the shared emissions resolver |
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

## One ledger, one read-side verb

`abc report` is the canonical read-side surface over the local SQLite
runs ledger. The Layer-0/1/2 rate-card resolver (`internal/accounting`)
and the grid-intensity resolver are consumed directly here; the prior
`abc accounting report` and `abc emissions [report]` verbs were folded
into this single closed-loop output (spec
`cli-verb-tree-restructure`).

```
abc report --since=… --until=…
```

produces spend, emissions, and postdoc-hours-returned together, with
per-investigation rollups. For namespace-budget management (the
write-side, admission-gate surface), see
[`abc accounting`](./abc-accounting.md).

## Sample output

```
$ abc report
Your 2026 so far:
────────────────────────────────────────────────────
Questions explored (investigations):  3
Pipeline runs:                        12  (10 worked, 2 retried)
Total compute:                        47 CPU-hrs, 0 GPU-hrs

Spend this period:                    R 1,420
Emissions this period:                47.3 kg CO₂e

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

Rate card (effective):
  currency                            ZAR       built-in    (SA market default)
  cost.cpu_hour                       0.5       built-in    (abc-cluster-cli vX — SA on-prem indicative)
  cost.gpu_hour                       9         built-in    (abc-cluster-cli vX — SA on-prem indicative)
  cost.memory_gb_hour                 0.05      built-in    (abc-cluster-cli vX — SA on-prem indicative)
  cost.storage_scratch_gb_hour        0.0001    built-in    (amortised enterprise NVMe + power)
  cost.postdoc_per_hour               350       built-in    (HSRC 2025 SA postdoctoral compensation guidance)
  emissions.grid_factor_gco2_per_kwh  900       built-in    (Eskom Integrated Annual Report 2023)
  emissions.cpu_w                     12        built-in    (Cloud Carbon Footprint v3 coefficient set)
  emissions.gpu_w                     250       built-in    (Cloud Carbon Footprint v3 coefficient set)
  emissions.memory_gb_w               0.3725    built-in    (Cloud Carbon Footprint v3 coefficient set)
  emissions.pue                       1.5       built-in    (Uptime Institute 2023 — generic on-prem average)
  emissions.storage_scratch_w_per_tb  8         built-in    (Samsung PM9A3 envelope amortised)

These rates are showback estimates; not invoice-grade. To override:
  abc config accounting set cost.postdoc_per_hour=400
  abc config emissions set pue=1.27 grid_factor_gco2_per_kwh=950
```

The provenance footer is generated from the resolved rate card — every
value carries its layer (`built-in` / `local` / `flag`) and citation. A
Layer-1 override in `~/.abc/config.yaml` (e.g. `cost.postdoc_per_hour:
525`) flows through the same path the other two verbs use, so the
postdoc rate displayed here is the same value `abc accounting --by=user`
would multiply against. One ledger, three lenses.

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
    "currency":                          {"value": "ZAR",   "source": "built-in", "citation": "SA market default"},
    "cost.cpu_hour":                     {"value": 0.5,     "source": "built-in", "citation": "abc-cluster-cli vX — SA on-prem indicative"},
    "cost.gpu_hour":                     {"value": 9,       "source": "built-in", "citation": "..."},
    "cost.memory_gb_hour":               {"value": 0.05,    "source": "built-in", "citation": "..."},
    "cost.storage_scratch_gb_hour":      {"value": 0.0001,  "source": "built-in", "citation": "..."},
    "cost.postdoc_per_hour":             {"value": 350,     "source": "built-in", "citation": "HSRC 2025 SA postdoctoral compensation guidance"},
    "emissions.grid_factor_gco2_per_kwh": {"value": 900,    "source": "built-in", "citation": "Eskom IAR 2023"},
    "emissions.cpu_w":                   {"value": 12,      "source": "built-in", "citation": "CCF v3"},
    "emissions.gpu_w":                   {"value": 250,     "source": "built-in", "citation": "CCF v3"},
    "emissions.memory_gb_w":             {"value": 0.3725,  "source": "built-in", "citation": "CCF v3"},
    "emissions.pue":                     {"value": 1.5,     "source": "built-in", "citation": "Uptime Institute 2023"},
    "emissions.storage_scratch_w_per_tb": {"value": 8,      "source": "built-in", "citation": "Samsung PM9A3 envelope"}
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

- [`abc accounting`](./abc-accounting) — write-side namespace budget caps and
  admission-gate thresholds (grove+ / cloud).
- [`abc localdb status`](./local-state) — schema version, applied migrations,
  feature flags.
