---
title: Seedling tier
---

# Seedling tier — operator guide

The **seedling tier** is a minimal, self-contained abc-cluster deployment
designed for:

- A single research group at a university or institute.
- A course or workshop environment with 20–100 participants.
- A pilot / proof-of-concept before scaling to the grove tier.

It runs on a single Linux VM (or a small bare-metal node). Nomad handles
job scheduling; MinIO provides object storage. A reverse proxy (Caddy or
equivalent) is optional — useful for internet-facing deployments that need
TLS, but not required on private LANs or HPC networks. Access is managed via a claim-code system — users arrive
at the landing page, enter a code issued by the operator, and immediately
receive CLI credentials with no account registration required.

## Architecture summary

| Component | Role | Technology |
|---|---|---|
| Edge VM | File serving, /api/* routing | Caddy 2 (optional) or Python http.server |
| Job scheduler | Run user workloads | HashiCorp Nomad |
| Object storage | Data buckets per group | MinIO |
| Access control | Namespace + bucket ACLs | Nomad ACL + MinIO policy |
| Claim service | Issue credentials on code redemption | `claim_server.py` (Nomad exec job) |
| Landing page | User onboarding + docs entry | Static HTML + `landing.js` |
| Operator network | SSH + admin UIs | Tailscale (recommended) |

## Deployment checklist

Work through the sections in order. Each step depends on the previous one.

1. **[Provision the access pool](provision)** — run `provision.py add` to mint
   Nomad tokens + MinIO users and store them in `abc-landing.db`.
2. **[Deploy landing + claim service](deploy)** — submit the Nomad job and
   run `deploy-landing.sh` to go live.
3. **[Reverse proxy / TLS](caddy)** — optional: set up Caddy, plain HTTP, or upstream TLS.
4. **[Issue handover files](handover)** — use `abc auth context show yaml`
   to export per-user config snippets (for named admins or group leads).

## Access model

The seedling tier uses **pseudonymous pooled accounts**:

- Each slot has a random handle (`brave_klipspringer`, `calm_springbok`, …).
- The operator pre-provisions a pool of slots with Nomad tokens and MinIO keys.
- A claim code is distributed to each participant (email, slide, printed card).
- When the participant redeems their code, they receive a `config.yaml` download
  with their slot's credentials — shown once, not stored server-side.

This model avoids email/password account management while maintaining
namespace isolation between groups. No PII is required; the landing page
only asks for a name and email for consent logging.

## Namespace layout

Each group gets its own Nomad namespace (`su-<group>`) and MinIO bucket group
(`g-su-<group>`). Users within a group share the namespace and bucket but
cannot see other groups' jobs or data.

```
Nomad namespaces:
  default         ← admin jobs (abc-landing-api, minio, ...)
  su-lab1         ← group lab1 pool tokens
  su-lab2         ← group lab2 pool tokens

MinIO groups:
  g-su-lab1       ← MinIO policy: read/write own-group bucket
  g-su-lab2
```

## Next step

→ [Provision the access pool](provision)
