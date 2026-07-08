---
slug: /
title: Cluster Setup
---

# Cluster Setup

Operator documentation for deploying and running a private **abc-cluster** instance
on the **seedling** tier — a single Linux server (or small cluster) with no
control plane: CLI, Nomad, Tailscale, MinIO/RustFS, and tusd.

Seedling has two sub-tiers:

| Sub-tier | What it adds |
|---|---|
| **Seedling** | Floor automation only — CLI, Nomad, Tailscale, MinIO/RustFS, tusd |
| **Seedling-enhanced** | Production hardening — Traefik, observability stack, ntfy, Boundary worker, abc-backups |

## Seedling tier pages

| Page | Summary |
|---|---|
| [Seedling overview](seedling/) | Architecture, phased checklist, namespace layout |
| [Phase 1 — Bare Nomad](seedling/nomad) | Install Nomad, bootstrap ACL, verify |
| [Phase 2 — Pulumi bootstrap](seedling/bootstrap) | Deploy the full abc-seedling stack via Pulumi |
| [Phase 3 — Provision access pool](seedling/provision) | Mint tokens and populate the claim database |
| [Phase 4 — Deploy landing page](seedling/deploy) | Run deploy-landing.sh; go live |
| [Reverse proxy / TLS (optional)](seedling/caddy) | Caddy for internet-facing clusters; plain HTTP for LANs |
| [Issuing handover files](seedling/handover) | Export config.yaml snippets for named users |

> **Looking for the CLI docs?**
> The **Cluster CLI** section covers the `abc` command-line tool — installation,
> the command structure, and the full reference. Start at [CLI → Overview](/cli/).
