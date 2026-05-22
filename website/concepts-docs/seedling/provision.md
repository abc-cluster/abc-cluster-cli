---
title: Provision the access pool
---

# Provision the access pool

Before users can claim access, you need to mint Nomad tokens and MinIO users
for each slot and store them in the SQLite database. `provision.py` does this
in a single command per group.

## Prerequisites

- A running Nomad cluster with ACLs enabled and an admin/management token.
- A running MinIO instance with a root (admin) user.
- The `mc` (MinIO Client) CLI installed and accessible as `mc` on your PATH.
- Python 3.8+ (stdlib only — no pip install needed for `provision.py`).
- The `abc-seedling-template/claim-server/` directory.

## Provision slots for a group

```bash
python3 claim-server/provision.py add \
  --db         /data/abc-landing.db \
  --group      lab1 \
  --count      20 \
  --nomad-addr https://nomad.your-cluster.example.com \
  --nomad-token <admin-nomad-token> \
  --minio-endpoint https://s3.your-cluster.example.com \
  --minio-root-user root \
  --minio-root-password <root-password>
```

For each of the 20 slots this command:

1. Generates a pseudonymous handle (`{adj}_{animal}`, e.g. `calm_springbok`).
2. Generates a 20-character random claim code.
3. POSTs to Nomad `/v1/acl/token` → captures `AccessorID` + `SecretID`.
4. Runs `mc admin user add` + `mc admin group add su-lab1 <username>`.
5. INSERTs all credentials into `account_pool` in the SQLite database.

If any step fails (Nomad 403, MinIO unreachable, DB collision), the
already-created credentials are rolled back before the script continues
to the next slot.

**Repeat for each group:**

```bash
python3 claim-server/provision.py add --group lab2 --count 15 ...
python3 claim-server/provision.py add --group demo --count 5 ...
```

## Check pool state

```bash
python3 claim-server/provision.py list --db /data/abc-landing.db
```

Output:

```
slot_name                      group        state      claimed_by                     claimed_at
brave_klipspringer             lab1         unclaimed
calm_springbok                 lab1         unclaimed
bold_klipspringer              lab1         claimed    user@example.com               2026-05-22
...

Total: 20  unclaimed: 18  claimed: 2
```

## Import from a Supabase export

If you are migrating an existing deployment that used Supabase, export the
`account_pool` table to JSON (e.g. via the Supabase dashboard → Table editor
→ Download CSV, then convert) and import:

```bash
python3 claim-server/provision.py import \
  --db   /data/abc-landing.db \
  --file slots.json
```

Expected JSON format:

```json
[
  {
    "slot_name":             "brave_klipspringer",
    "group_name":            "lab1",
    "claim_code":            "ABCDE12345FGHIJ67890",
    "nomad_token_accessor":  "...",
    "nomad_token_secret":    "...",
    "minio_user":            "pool-abc123",
    "minio_access_key":      "pool-abc123",
    "minio_secret_key":      "...",
    "state":                 "unclaimed"
  }
]
```

## Distribute claim codes

The `provision.py list` output shows claim codes. Distribute them to
participants however makes sense for your deployment:

- **Course**: include in the onboarding email or printed handout.
- **Demo**: print on a slide or whiteboard at the start of the session.
- **Group lab**: share per-group codes with the group lead who distributes
  within the group.

Codes are 20 characters, alphanumeric, case-sensitive.

## ACL policy requirements

`provision.py` creates Nomad tokens with the following capability set on
the `su-<group>` namespace:

```
submit-job · dispatch-job · read-job · list-jobs
read-logs · read-fs · alloc-exec · alloc-node-exec
```

This is the minimum set required for `abc job run`, `abc job logs`,
and `abc data upload`. Users cannot read other groups' jobs.

The Nomad admin token used by `provision.py` must have ACL management
permissions (the management token or a token with `acl:write`).

## Next step

→ [Deploy landing + claim service](deploy)
