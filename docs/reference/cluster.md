---
sidebar_position: 10
---

# cluster

Sync and inspect cluster capability metadata.

> **Note:** most `abc cluster *` operations require `--cloud` and an
> infrastructure-tier token. **`abc cluster capabilities {sync,show}`
> is the exception** and runs against any Nomad endpoint — including
> pre-cloud seedling and grove deployments — so capability discovery
> works before a cloud account exists.

## cluster capabilities sync

Probe the cluster's capability surface and update the active context's
`capabilities` block in `~/.abc/config.yaml`.

```bash
abc cluster capabilities sync
```

### Discovery cascade

The probe source is determined by what's configured for the active
context:

1. **`controller_url` set** → probe abc-controller-svc's
   `/v1/capabilities` (canonical when present).
   **No fallback to Nomad on failure** — preserves the trust boundary.
2. **`controller_url` empty** → probe Nomad services API (with
   job-listing fallback on 403). This is the pre-controller / seedling
   / grove path used today.

Forced cascade entry point (debug / testing):

```bash
abc cluster capabilities sync --source=controller    # force controller probe
abc cluster capabilities sync --source=nomad         # force Nomad introspection
abc cluster capabilities sync --source=tier-default  # skip probe; seed from cluster_type
```

### Schema + freshness

The stored block carries:

- `last_synced` — timestamp; the freshness layer treats values within
  10 minutes as fresh, between 10 min and 24 hr as
  revalidate-in-background, beyond 24 hr as blocking-probe.
- `schema_version` — currently `1`. Evolves additively forever; v2
  requires an ADR.
- `services` — keyed by **technical name** (`abc-bitemporal-svc`,
  `abc-policy-svc`, `abc-accounting-svc`, etc.) with `available`,
  `version`, `features[]`, `endpoints{}`, `reason`, `fallback`.
- `probe_source` — `controller-aggregate` | `nomad-introspection` |
  `tier-default` | `pulumi-snapshot` (Phase 1.D).

Env knobs:

- `ABC_CAPABILITY_TTL=<minutes>` — override foreground TTL (default 10).
- `ABC_CAPABILITY_HARD_EXPIRY=<hours>` — override hard expiry (default 24).
- `ABC_NO_PROBE=1` — disable probing for the session; cache + tier-default only.

## cluster capabilities show

Display the cached capabilities for the active cluster:

```bash
abc cluster capabilities show
```

Outputs a structured table of: available datacenters, node drivers
(containerd, docker), installed service versions, and feature flags.

## report / accounting / compliance

High-level cluster reporting commands. **`abc report`** is the read-side
showback (spend, emissions, hours saved) and works at every tier from
`~/.abc/local.db`. **`abc accounting`** is the write-side budget surface
and requires grove+/cloud (gated by the capability layer):

```bash
abc report                                # closed-loop showback (all tiers)
abc --cloud accounting budget list        # cloud namespace cap list
abc --cloud accounting budget set \
    --namespace=genpath --monthly=50000   # set a cap
abc compliance --cloud                    # compliance status summary
```

`--all-contexts` (cross-context aggregation) on `abc report` is gated by
the capability layer: passing it when the controller service isn't
deployed produces a clear error pointing at the required service rather
than a silent "not implemented".

For the per-metric showback table and JSON schema, see
[`abc report`](./abc-report.md). For namespace cap management, see
[`abc accounting`](./abc-accounting.md).
