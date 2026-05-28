---
slug: /
title: Cluster Setup
---

# Cluster Setup

Operator documentation for deploying and running a private **abc-cluster** instance.

abc-cluster is available in three tiers that describe the scale and feature surface
of a deployment:

| Tier | Scale | Access management | Object storage |
|---|---|---|---|
| **Seedling** | Single node or small cluster | Claim-code pool (SQLite) | MinIO |
| **Grove** | Multi-node, multi-institution | Managed accounts | MinIO / S3 |
| **Garden** | Federation, cloud-hybrid | Full IAM | Cloud-native |

This documentation covers the **seedling tier**. Grove and garden are in progress.

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
