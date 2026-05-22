---
title: Seedling tier
---

# Seedling tier — operator guide

The **seedling tier** is a minimal, self-contained abc-cluster deployment designed
for a single research group, a course, or an institutional pilot. It runs on a
single Linux node (VM or bare-metal). Nomad handles job scheduling; MinIO provides
object storage; a Python claim service issues credentials via a landing page.

## Architecture

```
users
  │  HTTP or HTTPS (TLS optional — not required on LANs)
  ▼
[reverse proxy]  ── /api/* ──▶  [Nomad job: abc-landing-api]
(optional: Caddy,                        │
 plain HTTP on LAN)                      ▼
  │                             [SQLite: abc-landing.db]
  │  /                           claim codes · credentials
  ├──▶  landing page (static HTML)
  │
  │  /docs/
  └──▶  abc CLI documentation

[Nomad]   ──▶  user workload jobs
[MinIO]   ──▶  data buckets (one per group)
[Tailscale]    operator SSH + admin UIs (recommended)
```

## Component overview

| Component | Role | Required? |
|---|---|---|
| Nomad | Job scheduling | Yes — the core primitive |
| MinIO | Object storage per group | Yes — installed via Pulumi |
| tusd | Resumable browser uploads | Yes — installed via Pulumi |
| abc-landing-api | Claim service (Python + SQLite) | Yes — runs as Nomad job |
| Landing page | User onboarding (static HTML) | Yes |
| Reverse proxy | TLS termination, /api/* routing | **Optional** — not required on private LANs |
| Grafana + Prometheus | Monitoring | Optional — installed via Pulumi |

## Phased deployment

The deployment is split into four phases that build on each other. Start with
Phase 1 even if you have an existing Nomad install — the checklist covers the
ACL and namespace configuration abc-cluster depends on.

### Phase 1 — Bare Nomad

Get a single Nomad node running with ACLs enabled. This is the only mandatory
prerequisite before Phase 2.

→ [Phase 1: Bare Nomad setup](nomad)

### Phase 2 — Pulumi bootstrap

Run the `abc-seedling` Pulumi stack from the `abc-deployments` repository.
This installs all abc services on top of the running Nomad node: MinIO, tusd,
the monitoring stack, and supporting Nomad jobs.

→ [Phase 2: Pulumi bootstrap](bootstrap)

### Phase 3 — Provision the access pool

With MinIO and Nomad running, mint Nomad tokens + MinIO users for each group
and populate the claim database. Users cannot claim access until this is done.

→ [Phase 3: Provision the access pool](provision)

### Phase 4 — Deploy the landing page

Substitute cluster-specific values into the landing page template and copy it
to the node. The claim service Nomad job starts serving `/api/claim`.

→ [Phase 4: Deploy the landing page](deploy)

### Optional — Reverse proxy

For internet-facing deployments, add Caddy to handle TLS automatically.
On a private LAN or HPC network, plain HTTP is sufficient.

→ [Reverse proxy / TLS (optional)](caddy)

## Namespace layout

Each group gets an isolated Nomad namespace and MinIO bucket group:

```
Nomad namespaces
  default             ← admin jobs (abc-landing-api, minio, monitoring, …)
  su-<group>          ← pool tokens for each group

MinIO groups
  g-su-<group>        ← read/write policy on the group's bucket
```

Pool tokens can submit and inspect jobs in their own namespace only.
They cannot list nodes, inspect other groups' jobs, or read the default namespace.
