---
id: abc-accounting
title: abc accounting
sidebar_position: 11
---

# `abc accounting`

> For showback / cost reporting, see [`abc report`](./abc-report.md). This page
> covers the **write-side** budget management surface only.

`abc accounting` is the namespace-budget management surface — monthly spend
caps and admission-gate thresholds, enforced by Jurist (`abc-policy-svc`)
and stored by the controller service (`abc-controller-svc`). The commands are:

```
abc accounting {list, set, show}
```

Available at the **grove tier and above**. At seedling the commands reject
with the standard capability message:

```
abc accounting requires abc-controller-svc; not available in this
context. Configure controller_url to point at a grove-tier deployment.
```

## Synopsis

```
abc accounting list
abc accounting show --namespace=<name>
abc accounting set  --namespace=<name> --monthly=<amount> [flags]
```

The commands require `controller_url` to be set on the active context (a
grove-tier deployment of abc-controller-svc). Admission-side enforcement
happens in Jurist regardless of which client wrote the cap.

## `abc accounting list`

Lists every namespace cap visible to the caller, with the current spend
and admission-gate status.

```
$ abc accounting list
  NAMESPACE                CAP/MONTH    CURRENT SPEND  CCY      STATUS
  ──────────────────────────────────────────────────────────────────────────
  genpath                  50000.00     12420.50       ZAR      ok
  su-mbhg-bioinformatics   25000.00     24180.00       ZAR      alerting
  internal-test            unlimited    340.00         ZAR      ok
```

`STATUS` is computed by the gateway from `current_spend / monthly_cap`
relative to the namespace's `alert_at` and `block_at` thresholds. A
`blocked` namespace will reject new submissions at admission.

## `abc accounting show --namespace=<name>`

Detailed view for a single namespace, including the configured alert
and block thresholds.

```
$ abc accounting show --namespace=genpath
  Namespace      genpath
  Cap            50000.00 ZAR/month
  Current spend  12420.50 ZAR
  Status         ok
  Alert at       80%
  Block at       100%
```

| Flag | Default | Description |
|---|---|---|
| `--namespace=<name>` | `$ABC_NAMESPACE` / `$NOMAD_NAMESPACE` | Namespace to query (required) |

## `abc accounting set`

Creates or updates a namespace cap. The cap is the monthly spend ceiling
in the workspace currency; `--alert-at` and `--block-at` are fractions
of that ceiling.

```
$ abc accounting set \
    --namespace=genpath --monthly=75000 --currency=ZAR \
    --alert-at=0.8 --block-at=1.0
  Budget cap for "genpath" set to 75000.00 ZAR/month.
```

To remove a cap (make the namespace unlimited), pass `--monthly=0`:

```
$ abc accounting set --namespace=genpath --monthly=0
  Budget cap for "genpath" removed (unlimited).
```

| Flag | Default | Description |
|---|---|---|
| `--namespace=<name>` | `$ABC_NAMESPACE` / `$NOMAD_NAMESPACE` | Namespace to configure (required) |
| `--monthly=<amount>` | `0` | Monthly cap in `--currency` (`0` = unlimited) |
| `--currency=<code>` | `USD` | ISO-4217 alpha currency code |
| `--alert-at=<frac>` | `0.8` | Alert when spend reaches this fraction (0.0–1.0) |
| `--block-at=<frac>` | `1.0` | Block new submissions at this fraction (0.0–1.0) |

The inline alert thresholds on `budget set` are v1; an `abc accounting
alerts` extraction lands when alert-management gains its own write-side
surface.

## Capability requirements

Every `budget` subverb declares:

```
Required{ AllOf: [{abc-controller-svc}, {abc-policy-svc}] }
```

At seedling neither service is in the active context's capability map,
and the command rejects with the standard `capability.Require()` message
before any network call.

## Future commands (deferred)

These appear in the verb-tree restructure spec as out-of-scope for v1:

- `abc accounting forecast` — bitemporal projection (Kayastha).
- `abc accounting close` — end-of-period close + sign + lock.
- `abc accounting export --funder=<id>` — funder-format export.
- `abc accounting reconcile` — cross-grove ledger reconcile.
- `abc accounting alerts` — extracted alert-management subverb.

Each ships in its own spec when the underlying feature matures.

## See also

- [`abc report`](./abc-report.md) — read-side showback (spend, emissions,
  hours saved). Closed-loop default output, all tiers.
- [`abc config accounting`](./global-flags.md) — per-context rate-card
  configuration consumed by `abc report`.
