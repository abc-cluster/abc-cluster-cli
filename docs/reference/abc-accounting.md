---
id: abc-accounting
title: abc accounting
sidebar_position: 11
---

# `abc accounting`

Per-context cost report over the local `~/.abc/local.db` `runs` table,
multiplied by a layered rate card. Showback estimates only — not
invoice-grade.

When invoked with no subcommand, runs the local SQLite report. The
existing cloud-budget verbs (`abc accounting list/show/set --cloud`)
remain available for cloud-tenancy users.

## Synopsis

```
abc accounting [flags]
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--by=<axis>` | `namespace` | Group-by axis: `namespace`, `project`, `investigation`, `user`, `pipeline` |
| `--since=YYYY-MM-DD` | 30 days ago | Window start |
| `--until=YYYY-MM-DD` | now | Window end (inclusive end-of-day) |
| `--currency=<code>` | from rate card | ISO-4217 alpha override |
| `--output=table\|csv\|json` | `table` | Output format |
| `--rate-source=full\|brief\|none` | `full` (table) / `none` (csv) | Footer verbosity |
| `--include-incomplete` | off | Include `status='running'` rows |
| `--rate-cpu-hour=N` | — | Layer 2 cost-rate override (per CPU·hour) |
| `--rate-gpu-hour=N` | — | Layer 2 cost-rate override (per GPU·hour) |
| `--rate-memory-gb-hour=N` | — | Layer 2 cost-rate override (per GB·hour) |
| `--all-contexts` | rejected | Phase 2 — currently errors with a clear message |

## Storage in accounting

Cost includes **scratch storage** (transient, per-run) when runs were
submitted with a `--scratch-gb=<N>` flag on `abc {pipeline,job,module}
run`. The scratch contribution to a run's total is

```
scratch_gb_hours = scratch_gb × walltime_hours
scratch_cost     = scratch_gb_hours × cost.storage_scratch_gb_hour
```

mirroring how GPU hours are derived from `gpu_count × walltime`.

**Persistent storage** (output buckets, results that survive past the
producing run) is not in the per-run total. It is project-scoped and
lives under a future `abc accounting storage` verb that joins against
periodic `project_storage_snapshots` captures. Charging a run for the
perpetual footprint of its outputs would produce nonsense numbers; the
producing run completed in hours, the data sits for years.

The default Layer 0 SA constants are documented in
`design/exploring/permissions-accounting.md` and
`brainstorms/emissions-accounting/2026-05-07-storage-accounting.md`.

## Worked example

```
$ abc accounting --by=namespace --since=2026-04-01

Namespace                Cost (ZAR)
----------------------------------------
su-mbhg-bioinformatics       124.50
su-mbhg-hostgen                78.20

Rate card (effective):
  cost.cpu_hour                 0.45    config     (~/.abc/config.yaml mtime 2026-05-06 14:23)
  cost.gpu_hour                 9       built-in   (abc-cluster-cli v0.1.25 — SA on-prem indicative, 2026-05-07)
  cost.memory_gb_hour           0.05    built-in   (abc-cluster-cli v0.1.25 — SA on-prem indicative, 2026-05-07)
  cost.storage_scratch_gb_hour  0.0001  built-in   (amortised enterprise NVMe + power, 2026-05-07)
  currency                      ZAR     built-in   (SA market default)

These rates are showback estimates; not invoice-grade. To override:
  abc config accounting set cost.cpu_hour=0.55
```

## Three sources of rates

| Source | Where | Citation in footer |
|---|---|---|
| `built-in` | Hardcoded SA on-prem indicative constants in the CLI binary | Citation string with the underlying source (Eskom IAR, Cloud Carbon Footprint, Uptime Institute) and the CLI release date |
| `config` | `~/.abc/config.yaml` per-context `accounting:` block | Mtime of the config file at report time |
| `flag` | `--rate-cpu-hour=…`, `--rate-gpu-hour=…`, `--currency=…` | "this invocation" — non-reproducible |

Override precedence: flags beat config beat built-in.

## Cost formula

```
cost = cpu_hours       × rate.cpu_hour
     + gpu_hours       × rate.gpu_hour
     + memory_gb_hours × rate.memory_gb_hour

where gpu_hours = gpu_count × walltime_hours
```

`cpu_hours` and `memory_gb_hours` come from the run record (populated by
the run-completion watcher). `gpu_hours` is derived at report time;
there is no `gpu_hours` column in `runs`.

## Output formats

* `--output=table` (default) — fixed-width text plus a "Rate card
  (effective)" footer.
* `--output=csv` — RFC 4180 with header row; the rate-card footer is
  suppressed (`--rate-source=none` is the default for CSV).
* `--output=json` — pretty-printed JSON; always includes a `rate_card`
  key with the full provenance regardless of `--rate-source`.

## See also

* [`abc emissions`](abc-emissions.md) — same shape, kg CO₂e instead of currency
* [`abc config accounting`](#) — manage per-context cost rates
* [Local state](local-state.md) — `runs` table schema
