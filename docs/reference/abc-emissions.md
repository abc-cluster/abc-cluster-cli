---
id: abc-emissions
title: abc emissions
sidebar_position: 12
---

# `abc emissions`

Per-context carbon-emissions report (kg CO₂e) over the local
`~/.abc/local.db` `runs` table, multiplied by a layered emissions rate
card.

When invoked with no `--cloud`, runs the local SQLite report. Pass
`--cloud` to fetch the legacy `GET /v1/emissions` API instead.

## Synopsis

```
abc emissions [flags]
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--by=<axis>` | `namespace` | Group-by axis: `namespace`, `project`, `investigation`, `user`, `pipeline` |
| `--since=YYYY-MM-DD` | 30 days ago | Window start |
| `--until=YYYY-MM-DD` | now | Window end |
| `--unit=kg\|t\|g` | `kg` | Display unit |
| `--output=table\|csv\|json` | `table` | Output format |
| `--rate-source=full\|brief\|none` | `full` (table) / `none` (csv) | Footer verbosity |
| `--include-incomplete` | off | Include `status='running'` rows |
| `--pue=N` | — | Layer 2 PUE override (1.0 ≤ N ≤ 3.0) |
| `--grid-factor=N` | — | Layer 2 grid factor override (g CO₂e/kWh, 0–2000) |
| `--cpu-w=N` | — | Layer 2 watts-per-CPU override |
| `--gpu-w=N` | — | Layer 2 watts-per-GPU override |
| `--memory-gb-w=N` | — | Layer 2 watts-per-GB-DRAM override |
| `--all-contexts` | rejected | Phase 2 |
| `--cloud` | off | Use legacy cloud `/v1/emissions` API |

## Canonical formula

Per
[`design/exploring/carbon-accounting-context.md`](https://github.com/abc-cluster/abc-cluster-cli):

```
Energy_kWh = ((cpu_hours      × cpu_w
            + gpu_hours       × gpu_w
            + memory_gb_hours × memory_gb_w) / 1000
            + scratch_gb_hours × storage_scratch_w_per_tb / 1000 / 1000
           ) × pue

CO2_kg     = Energy_kWh × grid_factor_gco2_per_kwh / 1000
```

The scratch term divides by 1000 twice — once to convert TB to GB, and
once to convert W·hour to kWh. Scratch is included only when runs were
submitted with `--scratch-gb=<N>` (the future run-watcher will overwrite
with the actual provisioned `disk_mb` from the Nomad alloc).

Source citations for the default coefficients:

| Coefficient | Default | Source |
|---|---|---|
| `cpu_w` | 12 | Cloud Carbon Footprint v3 coefficient set |
| `gpu_w` | 250 | Cloud Carbon Footprint v3 coefficient set |
| `memory_gb_w` | 0.3725 | Cloud Carbon Footprint v3 coefficient set |
| `pue` | 1.5 | Uptime Institute 2023 Global Data Center Survey — generic on-prem average |
| `grid_factor_gco2_per_kwh` | 900 | Eskom Integrated Annual Report 2023 (Greentech VP doc 2024-09) |
| `storage_scratch_w_per_tb` | 8 | Samsung PM9A3 active envelope amortised over capacity |
| `storage_persistent_w_per_tb` | 4 | WD Ultrastar DC HC560 idle-dominated |
| `storage_ec_amplification` | 1.33 | RustFS 3+1 erasure coding default |

Persistent storage is project-scoped, not per-run. It surfaces under a
future `abc emissions storage` verb that joins against periodic
`project_storage_snapshots` captures — see
[`design/exploring/permissions-accounting.md`](#) and
[`brainstorms/emissions-accounting/2026-05-07-storage-accounting.md`](#).

## Worked example

```
$ abc emissions --by=namespace --since=2026-04-01

Namespace                Emissions (kg CO2e)
---------------------------------------------
su-mbhg-bioinformatics       14.2

Rate card (effective):
  emissions.grid_factor    900     built-in  (Eskom Integrated Annual Report 2023; refreshed 2026-05-07)
  emissions.cpu_w          12      built-in  (Cloud Carbon Footprint v3 coefficient set; refreshed 2026-05-07)
  emissions.gpu_w          250     built-in  (Cloud Carbon Footprint v3 coefficient set; refreshed 2026-05-07)
  emissions.memory_gb_w    0.3725  built-in  (Cloud Carbon Footprint v3 coefficient set; refreshed 2026-05-07)
  emissions.pue            1.5     built-in  (Uptime Institute 2023 Global Data Center Survey — generic on-prem average; refreshed 2026-05-07)

These rates are estimates; the SA grid factor varies by hour and season.
For grant-justification or carbon-footprint disclosure use cases, override:
  abc config emissions set pue=1.27 grid_factor_gco2_per_kwh=950
```

## See also

* [`abc accounting`](abc-accounting.md) — same shape, currency instead of CO₂e
* [`abc config emissions`](#) — manage per-context emissions coefficients
