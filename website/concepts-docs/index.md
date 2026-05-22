---
slug: /
title: Cluster Setup
---

# Cluster Setup

Operator documentation for deploying and running a private **abc-cluster** instance.

abc-cluster is available in three tiers (seedling → grove → garden) that describe
the scale and feature surface of a deployment. This documentation covers the
**seedling tier** — a single-node or small-cluster deployment with Nomad + MinIO +
Caddy, suitable for a research group, a course, or a pilot at a new institution.

## What's covered here

| Section | Audience | Summary |
|---|---|---|
| [Seedling tier](seedling/) | Operators | Full deployment guide: provision, deploy, operate |
| [Provision the access pool](seedling/provision) | Operators | Mint Nomad tokens + MinIO users; populate SQLite |
| [Deploy landing + claim service](seedling/deploy) | Operators | Run deploy-landing.sh and the Nomad claim job |
| [Caddy configuration](seedling/caddy) | Operators | TLS, /api/* routing, security headers |
| [Issuing handover files](seedling/handover) | Group admins | Export config.yaml snippets for named users |

## What a seedling deployment looks like

```
internet
    │  HTTPS
    ▼
[Caddy edge VM]  ─── /api/* ──▶  [Nomad: abc-landing-api job]
    │                                       │
    │  /                                    ▼
    ├──▶  /var/www/<hostname>/          [SQLite: abc-landing.db]
    │      index.html  (landing page)      (claim codes + credentials)
    │      landing.js
    │
    │  /docs/
    └──▶  /var/www/<hostname>/docs/    (abc CLI docs, built separately)

[Nomad scheduler]  ──▶  workload jobs (user-submitted)
[MinIO storage]    ──▶  user data buckets (one per group)
[Tailscale mesh]   ──▶  operator access (SSH, admin UIs)
```

Users claim a pseudonymous slot via the landing page and receive a `config.yaml`
download with pre-populated Nomad token + MinIO credentials. No Supabase account
is required for a private deployment — the claim service is a stdlib-only Python
HTTP server running as a Nomad job.

## Before you start

You will need:

- A Linux VM (or bare-metal node) with a public IP and a registered DNS hostname.
- [Nomad](https://developer.hashicorp.com/nomad) installed and bootstrapped with ACLs enabled.
- [MinIO](https://min.io/) running (or `minio` as a Nomad job).
- [Caddy 2](https://caddyserver.com/) for TLS termination and reverse proxy.
- Python 3.8+ on both the deploy machine and the Nomad client node.
- `mc` (MinIO Client) CLI installed on the machine running `provision.py`.
- The `abc-seedling-template` directory from the `abc-deployments` repository.

:::tip Looking for the CLI docs?
The **Cluster CLI** section covers the `abc` command-line tool — installation,
the verb model, and the full reference. Start at [CLI → Overview](/cli/).
:::
