---
title: Phase 3 — Provision the access pool
---

# Phase 3 — Provision the access pool

Before users can claim access, you need to mint Nomad tokens and MinIO users
for each slot and store them in the SQLite database. `provision.py` does this
in a single command per group.

## Prerequisites

- Nomad running with ACLs and a management token (Phase 1).
- MinIO running with root credentials (Phase 2).
- The `mc` (MinIO Client) CLI installed on your deploy machine.
- Python 3.8+ (no pip dependencies — `provision.py` uses stdlib only).
- The `abc-seedling-template/claim-server/` directory from `abc-deployments`.

## Install mc (MinIO Client)

```bash
# Linux (amd64)
curl -fsSL https://dl.min.io/client/mc/release/linux-amd64/mc -o ~/bin/mc
chmod +x ~/bin/mc

# macOS
brew install minio/stable/mc
```

## Provision slots for a group

```bash
python3 claim-server/provision.py add \
  --db         /data/abc-landing.db \
  --group      lab1 \
  --count      20 \
  --nomad-addr https://nomad.your-cluster.example.com \
  --nomad-token <management-token> \
  --minio-endpoint https://s3.your-cluster.example.com \
  --minio-root-user root \
  --minio-root-password <root-password>
```

For each of the 20 slots this command:

1. Generates a pseudonymous handle (`{adj}_{animal}`, e.g. `calm_springbok`).
2. Generates a 20-character random claim code.
3. POSTs to Nomad `/v1/acl/token` → stores `AccessorID` + `SecretID`.
4. Runs `mc admin user add` + `mc admin group add su-lab1 <username>`.
5. INSERTs all credentials into `account_pool` in the SQLite database.

If any step fails, the already-created credentials are rolled back before
the script moves on to the next slot.

Repeat for each group:

```bash
python3 claim-server/provision.py add --group lab2 --count 15 ...
python3 claim-server/provision.py add --group demo  --count 5  ...
```

## Check pool state

```bash
python3 claim-server/provision.py list --db /data/abc-landing.db
```

```
slot_name                      group    state      claimed_by             claimed_at
brave_klipspringer             lab1     unclaimed
calm_springbok                 lab1     unclaimed
bold_klipspringer              lab1     claimed    user@example.com       2026-05-22

Total: 20  unclaimed: 18  claimed: 2
```

## Distribute claim codes

The `list` output shows the claim codes. Distribute them to participants
however fits your workflow:

- **Course** — include in the onboarding email or printed handout.
- **Workshop** — print on a slide or whiteboard at the start of the session.
- **Research group** — share per-group codes with the group lead.

Codes are 20 characters, alphanumeric, case-sensitive. Each code can only
be redeemed once.

## ACL policy — what pool tokens can do

Each pool token has write access to its own `su-<group>` namespace:

```
submit-job · dispatch-job · read-job · list-jobs
read-logs · read-fs · alloc-exec · alloc-node-exec · parse-job
```

Pool tokens cannot list nodes, access other groups' namespaces, or read
the `default` namespace. This is intentional — the namespace scoping is
what enforces isolation between groups.

## Copy the database to the cluster node

`provision.py` writes to a local SQLite file. Copy it to the node before
starting the claim service (Phase 4):

```bash
scp /data/abc-landing.db your-node:/data/abc-landing.db
```

Or run `provision.py` directly on the cluster node if you prefer.

## Next step

→ [Phase 4: Deploy the landing page](deploy)
