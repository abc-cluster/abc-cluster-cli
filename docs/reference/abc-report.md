# `abc report`

Produce a researcher productivity report from local state — runs, annotations,
and command history — for an investigation, a branch comparison, or a whole project.

No server calls. Everything comes from `~/.abc/local.db`.

## Usage

```
abc report [<investigation>]  [flags]
abc report --project=<name>   [flags]
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--project=<name>` | — | Project-level summary instead of single investigation |
| `--since=YYYY-MM-DD` | — | Restrict to runs and annotations on or after this date |
| `--trend` | `false` | Add a Trend section showing metrics bucketed by time period |
| `--period=weekly\|monthly` | `weekly` | Bucket size for `--trend` |
| `--format=table\|json\|csv` | `table` | Output format (`csv` with `--trend` for manuscript figures) |
| `--no-audit` | `false` | Exclude the Commands section (same as setting `ABC_NO_AUDIT=1`) |

## Output sections

### Single investigation (default)

When `<investigation>` has branches, a **branch comparison table** is rendered
at the top — columns per branch, rows for runs, MTTFS, walltime, CPU-hours,
issues logged, and dead-end reason.

```
╔══════════════════════════════════════════════════════════════════════════╗
║  abc report  ·  bright-otter-3                                          ║
║  Variant calling: GATK vs DeepVariant                                   ║
║  Project: tb-cohort-2026  ·  Context: bioinformatics  ·  41 days        ║
╚══════════════════════════════════════════════════════════════════════════╝

  Overview
  ────────────────────────────────────────────────────────────────────────
  Status       merged  ·  Decision: adopt DeepVariant
  Runs         12 total  ·  9 succeeded  ·  3 failed
  Walltime     28h 14m  ·  CPU-hours 112.4  ·  GPU-hours 4.1
  Branches     2  (1 dead-end, 1 merged)
  Citations    1  (→ warm-cedar-2)

  Branch comparison
  ────────────────────────────────────────────────────────────────────────
                         tidy-beaver-5       bright-otter-3 (main)
                         GATK v4.4           DeepVariant 1.6.1
                         ──────────────────  ───────────────────────
  Status                 dead-end            merged
  Runs                   7                   5
  Success / fail         5 / 2               4 / 1
  MTTFS                  3d 14h              1d 22h     ◀ 48% faster
  Walltime total         19h 42m             8h 32m     ◀ 57% less
  CPU-hours              78.2                34.2
  Issues logged          3                   1
  Dead-end reason        "CRAM index incompatibility; upstream bug"

  Timeline
  ────────────────────────────────────────────────────────────────────────
  2026-03-05  hypothesis   "GATK handles mixed-ploidy better than DV"
  2026-03-06  RUN ───────  nf-core/sarek@3.4.3  GATK  ✓ success  2h 48m
  2026-03-08  observation  "Q30 95.1%, het-call rate 0.82 on chr1 pilot"
  ...
  2026-04-12  decision     "Adopt DeepVariant 1.6.1"

  Productivity
  ────────────────────────────────────────────────────────────────────────
  Time-to-decision     38 days
  Runs before decision 12
  Issues per run       0.33
  Async run fraction   67%  (no follow-up command within 30 min of completion)
  Knowledge reuse      1 citation

  Commands  (cli_audit)
  ────────────────────────────────────────────────────────────────────────
  Total issued         47  ·  failed 3  (6%)
    investigation annotate  14  ████████████████
    pipeline run            12  ██████████████
    job logs                 8  █████████
```

### Project summary (`--project`)

```
$ abc report --project=tb-cohort-2026

  tb-cohort-2026  ·  investigation summary
  ──────────────────────────────────────────────────────────────────────
                     Runs  Success  MTTFS      Walltime   CPU-h  Annotations
  warm-cedar-2       6     5        2d 6h       9h 14m    38.1   9
  bright-otter-3    12     9        1d 22h     28h 14m   112.4   14
  tidy-beaver-5 [↳]  7     5        3d 14h     19h 42m    78.2   8
  quiet-falcon-9     3     1        — dead-end  2h 01m     8.1   5
  cosmic-pelican-7  (active — 4 runs so far)
```

## Derived metrics

All metrics are computed from `~/.abc/local.db` with no network calls.

| Metric | Source | Definition |
|---|---|---|
| MTTFS | `runs` | First `submitted_at` → first `completed_at` where `status='success'` per investigation |
| Failure-to-result ratio | `runs` | Failed runs ÷ total runs |
| Issues per run | `annotations`, `runs` | `COUNT(tag='issue')` ÷ `COUNT(runs)` |
| Time-to-decision | `annotations` | `decision.created_at` − `hypothesis.created_at` |
| Thinking gaps | `annotations`, `runs` | Δt between an observation/insight annotation and the next run submission |
| Async run fraction | `runs`, `cli_audit` | Runs with no `job logs`/`job status` command within 30 min of `completed_at` |
| Knowledge reuse | `citations` | Count of cross-investigation citation edges |

The **async run fraction** requires `cli_audit` to be populated. If `ABC_NO_AUDIT=1`
was set during the investigation period, or `--no-audit` is passed, this row is omitted.

## Annotation nudge

When `abc pipeline run` or `abc job run` completes and an active investigation is set,
the CLI prints a one-line tip if no annotation has been added in the last 4 hours:

```
[abc] Run RUN-01J… finished: ✓ success  (2h 48m)
      Tip: abc investigation annotate --tag=observation to record what you found.
      Disable: ABC_NO_ANNOTATION_TIP=1
```

## Environment variables

| Variable | Effect |
|---|---|
| `ABC_NO_AUDIT=1` | Disable `cli_audit` recording; Commands section omitted from `abc report` |
| `ABC_NO_ANNOTATION_TIP=1` | Suppress the post-run annotation nudge |

## Longitudinal trend (`--trend`)

Shows how metrics change week-over-week, revealing the reduction in accidental time as
the researcher builds trust in the platform.

```
$ abc report --project=tb-cohort-2026 --trend

  Trend  (weekly)
  ──────────────────────────────────────────────────────────────────────────
  Week         Runs  Fail%   MTTFS    Issues/run  Async%   Commands
  2026-03-02     3   67%     4d 2h      0.67        42%       18
  2026-03-09     2   50%     3d 6h      0.50        50%       12
  2026-03-16     3   33%     2d 1h      0.33        58%        9
  2026-03-23     2    0%     1d 8h      0.00        75%        7
  2026-03-30     1    0%       22h      0.00        83%        5
  ──────────────────────────────────────────────────────────────────────────
  Change        —   ↓67%    ↓46%      ↓100%        ↑41pp    ↓72%

  Scientific inquiry ratio  (Doing + Thinking) / (Watching + Overhead)
  ──────────────────────────────────────────────────────────────────────────
  Week         Doing   Watching   Thinking   Overhead   Ratio
  2026-03-02     5        9          2           2        0.78
  2026-03-09     4        5          3           0        1.17
  2026-03-16     5        3          3           0        2.67
  2026-03-23     4        2          2           1        2.00
  2026-03-30     3        1          2           0        5.00
  ──────────────────────────────────────────────────────────────────────────
  Trend        doing ↑, watching ↓  →  platform trust increasing
```

The **scientific inquiry ratio** classifies every `cli_audit` verb:

- **Doing** — `pipeline run`, `job run`, `module run`, `data upload`, `submit`
- **Watching** — `job logs`, `job status`, `pipeline runs`, `job list` (monitoring; accidental time)
- **Thinking** — `investigation annotate`, `investigation show`, `investigation tree` (recording insight; intrinsic)
- **Overhead** — `job stop`, `config`, `secrets`, `auth`, `cache` (platform friction)

A ratio above 1.0 means more time executing science and recording insight than monitoring
and fighting the platform. The trend of this ratio rising is the headline evidence for
platform value — in grant reports and in the academic manuscript.

Use `--format=csv --trend` to export week-by-week data for manuscript figure scripts:

```
abc report --project=tb-cohort-2026 --trend --format=csv > metrics.csv
```

## Tier availability

`abc report` is available at all tiers — it reads only from `~/.abc/local.db`
and never contacts the cluster. At abc-cloud (L5), the same metrics are
available in Metabase dashboards backed by the server-side researcher-event
table, which joins this local data with cluster-side telemetry when the user
opts in to data sharing.
