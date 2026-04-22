# Nomad ACL — Multi-Group Cluster Sharing (abc-nodes / aither)

Cluster: **aither** — `http://100.70.185.46:4646` — ACL enabled, single node.

This directory contains the ACL policy design and bootstrap scripts for sharing
the abc-nodes cluster between multiple research groups running Nextflow pipelines.
See `ACCESS_POLICY_PLAN.md` for the full policy specification including MinIO,
Vault, Grafana, and Wave.

Observability (Prometheus, Grafana dashboards, validation scripts): **`docs/abc-nodes-observability-and-operations.md`**.

---

## Design goals

| Goal | Mechanism |
|------|-----------|
| Isolate research-group jobs | One Nomad namespace per group |
| Protect infrastructure jobs | Dedicated **`abc-services`** namespace (admin-only; legacy name `services`) |
| Host shared platform apps (GVDS, BRIMS, …) | **`abc-applications`** namespace + narrow ACLs (see `ACCESS_POLICY_PLAN.md`) |
| Control who submits jobs | ACL policy per group × role |
| Prioritise between groups | `priority` field + batch preemption |
| Per-user S3 data isolation | MinIO `${aws:username}` IAM policy condition |
| Upload authentication | Traefik ForwardAuth → `abc-nodes-auth` |

> **Resource quotas** are a Nomad Enterprise feature. Fairness in OSS relies
> on job priority + preemption only.

---

## Directory layout

```
acl/
├── README.md                              # This file
├── ACCESS_POLICY_PLAN.md                  # Full policy specification
├── apply-su-mbhg.sh                       # Bootstrap script (run once)
├── setup-minio-namespace-buckets.sh       # Namespace bucket/policy + credential sync automation
├── apply-research-namespace-specs.sh      # nomad namespace apply for acl/namespaces/su-*.hcl
├── roles/
│   └── apply-roles.sh                     # Create/update Nomad ACL roles from policies
├── tokens.env                             # Generated token secrets (chmod 600, gitignored)
├── minio-credentials.env                  # Generated MinIO IAM credentials (chmod 600, gitignored)
├── server-preemption-patch.hcl            # Snippet to persist preemption in server HCL
│
├── namespaces/
│   ├── su-mbhg-bioinformatics.hcl         # Priority 70 (high)
│   └── su-mbhg-hostgen.hcl               # Priority 50 (normal)
│   # Planned: applications.hcl (platform apps namespace — see § Planned below)
│
├── policies/
│   ├── admin.hcl                          # Full cluster admin
│   ├── services-admin.hcl                 # services namespace admin (cluster-admin only)
│   ├── observer.hcl                       # Read-only, all namespaces
│   ├── su-mbhg-bioinformatics-group-admin.hcl
│   ├── su-mbhg-bioinformatics-submit.hcl
│   ├── su-mbhg-bioinformatics-member.hcl
│   ├── su-mbhg-hostgen-group-admin.hcl
│   ├── su-mbhg-hostgen-submit.hcl
│   └── su-mbhg-hostgen-member.hcl
│
├── minio-policies/
│   ├── su-mbhg-bioinformatics-group-admin.json
│   ├── su-mbhg-bioinformatics-member.json  # Per-user prefix isolation
│   ├── su-mbhg-hostgen-group-admin.json
│   ├── su-mbhg-hostgen-member.json
│   └── pipeline-service-account.json
│
├── tusd-auth/
│   ├── README.md                          # ForwardAuth service docs
│   ├── abc-nodes-auth.nomad.hcl           # Auth service job (→ services namespace)
│   └── traefik-nomad-auth.yml             # Traefik dynamic config
│
└── nextflow-configs/                      # Nextflow config templates per group
```

Example `~/.abc` multi-persona contexts (cluster-admin, group admin, group user): `../examples/abc-config.personas.yaml`.

---

## CLI contexts (`~/.abc/config.yaml`)

For day-to-day use, define **one context per Nomad identity** (different `nomad_token` + `nomad_namespace`). A worked example that matches this directory’s token names (`NOMAD_MGMT_TOKEN`, `NOMAD_TOKEN_BIO_ADMIN`, `NOMAD_TOKEN_BIO_ALICE`, …) lives at:

`examples/abc-config.personas.yaml` (under `deployments/abc-nodes/examples/`)

Copy it to `~/.abc/config.yaml` (or merge the `contexts:` entries), substitute real secrets, then `abc context use <name>` to switch persona.

---

## Namespace + priority model

```
services namespace      — cluster infrastructure (admin-only)
  priority 90           — core storage/proxy
  priority 80           — auth + notification helpers

applications namespace  — planned: platform apps (GVDS, BRIMS, …), not group pipelines
  priority TBD         — between infra helpers and group high (see ACCESS_POLICY_PLAN.md)

su-mbhg-bioinformatics  — research group, HIGH priority
  priority 70           — preempts hostgen batch jobs

su-mbhg-hostgen         — research group, NORMAL priority
  priority 50           — can be preempted by bioinformatics

(shared opportunistic)
  priority 30           — always preemptible
```

---

## Token + user naming

**Convention: `su-mbhg-<group>_<username>`**

The Nomad token Name equals the MinIO username. The Nomad token SecretID equals
the MinIO secret key. One credential pair per user, no extra distribution needed.

| Token / MinIO user | Nomad policy | MinIO policy |
|---|---|---|
| `su-mbhg-bioinformatics_submit` | `su-mbhg-bioinformatics-submit` | group-admin |
| `su-mbhg-bioinformatics_admin` | `su-mbhg-bioinformatics-group-admin` | group-admin |
| `su-mbhg-bioinformatics_alice` | `su-mbhg-bioinformatics-member` | member |
| `su-mbhg-hostgen_submit` | `su-mbhg-hostgen-submit` | group-admin |
| `su-mbhg-hostgen_admin` | `su-mbhg-hostgen-group-admin` | group-admin |
| `su-mbhg-hostgen_bob` | `su-mbhg-hostgen-member` | member |

---

## Step-by-step setup (already applied on aither)

### 1. Enable ACL (server restart required)

Add to each Nomad server HCL:
```hcl
acl { enabled = true }
```
Then `sudo systemctl restart nomad` on each node (rolling for multi-node).

### 2. Bootstrap the management token

```bash
nomad acl bootstrap
# Save the SecretID as your management token.
# Store it in Vault at secret/abc-nodes/nomad/management-token
# and wire into abc: abc config set contexts.min.admin.services.nomad.nomad_token <token>
```

### 3. Enable batch preemption

```bash
export NOMAD_ADDR=http://100.70.185.46:4646
export NOMAD_TOKEN=<management-token>
abc admin services nomad cli -- operator scheduler set-config \
  -preempt-batch-scheduler=true \
  -preempt-sysbatch-scheduler=true
```

### 4. Apply namespaces and policies

```bash
# Namespaces (note: no -f flag)
abc admin services nomad cli -- namespace apply acl/namespaces/su-mbhg-bioinformatics.hcl
abc admin services nomad cli -- namespace apply acl/namespaces/su-mbhg-hostgen.hcl

# Policies
for policy in services-admin observer \
  su-mbhg-bioinformatics-group-admin su-mbhg-bioinformatics-submit su-mbhg-bioinformatics-member \
  su-mbhg-hostgen-group-admin su-mbhg-hostgen-submit su-mbhg-hostgen-member; do
  abc admin services nomad cli -- acl policy apply \
    -description "$policy" "$policy" "acl/policies/${policy}.hcl"
done
```

### 5. Create tokens

```bash
# See acl/apply-su-mbhg.sh for the full bootstrap script.
bash acl/apply-su-mbhg.sh
# Tokens written to acl/tokens.env (chmod 600 — never commit)
```

### 5.1 Create/update ACL roles

```bash
# Idempotently creates base roles + per-group roles
ABC_ACTIVE_CONTEXT=abc-bootstrap \
bash deployments/abc-nodes/acl/roles/apply-roles.sh

# Optional: print token-to-role migration command templates
ABC_ACTIVE_CONTEXT=abc-bootstrap \
bash deployments/abc-nodes/acl/roles/apply-roles.sh --print-migration-commands
```

### 6. Set up MinIO

```bash
MINIO_USER=<root-user> MINIO_PASS=<root-pass> \
CLUSTER_ADMIN_IAM_USER=abc-cluster-admin \
CLUSTER_ADMIN_IAM_PASS='<strong-password>' \
SYNC_NOMAD_VARS=1 \
SYNC_VAULT=1 \
VAULT_ADDR=http://100.70.185.46:8200 \
VAULT_TOKEN=<vault-token> \
bash deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh
```

This automation now handles:

- bucket = namespace naming for all configured research namespaces
- user-scoped folder policies (`users/<user>/*`)
- group-admin full-bucket policies
- cluster-admin all-namespace policy
- credential persistence to:
  - `acl/minio-credentials.env` (local, gitignored)
  - Nomad variables: `nomad/jobs/abc-nodes-minio-iam/<principal>` (services namespace)
  - Vault KV v2 (optional): `secret/abc-nodes/minio-iam/<principal>`

### 6.1 Credential rotation playbook

Rotate MinIO IAM credentials by re-running the same automation with a new password prefix (or explicit cluster-admin password):

```bash
ABC_ACTIVE_CONTEXT=aither-bootstrap \
MINIO_USER=<root-user> MINIO_PASS=<root-pass> \
USER_PASSWORD_PREFIX='rot-2026-04-' \
CLUSTER_ADMIN_IAM_USER=abc-cluster-admin \
CLUSTER_ADMIN_IAM_PASS='<new-strong-password>' \
SYNC_NOMAD_VARS=1 \
SYNC_VAULT=1 \
VAULT_ADDR=http://100.70.185.46:8200 \
VAULT_TOKEN=<vault-token> \
bash deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh
```

What this refreshes:

- MinIO IAM user secrets (existing users are retained; policy attachments are re-applied)
- `acl/minio-credentials.env` local snapshot
- Nomad variable copies under `nomad/jobs/abc-nodes-minio-iam/<principal>`
- Vault KV copies under `secret/abc-nodes/minio-iam/<principal>` (if enabled)

Recommended post-rotation checks:

```bash
# 1) Verify MinIO users exist
~/.abc/binaries/mc admin user list sunminio

# 2) Verify one Nomad variable entry
ABC_ACTIVE_CONTEXT=aither-bootstrap \
abc admin services nomad cli -- var get -namespace services \
  nomad/jobs/abc-nodes-minio-iam/abc-cluster-admin
```

### 7. Deploy abc-nodes-auth

```bash
abc admin services nomad cli -- job run \
  deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl

# Place Traefik ForwardAuth config (hot-reload)
sudo cp acl/tusd-auth/traefik-nomad-auth.yml /etc/traefik/dynamic/
```

---

## `abc-applications` namespace

Shared platform workloads (examples: **GVDS**, **BRIMS**) use the **`abc-applications`** Nomad namespace so they stay separate from **`abc-services`** (core floor) and **`su-*`** research namespaces. Namespace HCL lives under `acl/namespaces/abc-applications.hcl`. Historical design notes remain in **`ACCESS_POLICY_PLAN.md`**.

---

## Adding a new research group

1. Copy a namespace HCL → `acl/namespaces/su-mbhg-<newgroup>.hcl`; set name, priority.
2. Copy the three policy HCLs → `acl/policies/su-mbhg-<newgroup>-{group-admin,submit,member}.hcl`; update namespace name.
3. Copy the two MinIO policy JSONs → `acl/minio-policies/su-mbhg-<newgroup>-{group-admin,member}.json`; update bucket name.
4. Apply all five files (namespace + 2 Nomad policies + 2 MinIO policies).
5. Create tokens and MinIO users following the convention above.
6. Extend `POLICY_MAP` in `deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl` and redeploy.

---

## Common operations

```bash
# List namespaces
abc admin services nomad cli -- namespace list

# List policies
abc admin services nomad cli -- acl policy list

# List tokens
abc admin services nomad cli -- acl token list

# Check running jobs in platform namespace
abc admin services nomad cli -- job status -namespace abc-services

# List MinIO users
~/.abc/binaries/mc admin user list sunminio

# Verify auth service
curl -si -H "X-Nomad-Token: $(grep BIO_ALICE acl/tokens.env | cut -d= -f2)" \
  http://100.70.185.46:9191/auth
```

---

## Optional: Node pools

Node pools (Nomad 1.6+) provide physical node isolation beyond priority.
Proposed split when additional nodes are added:

| Pool | Nodes | Purpose |
|------|-------|---------|
| `infra` | aither (and future infra node) | Services, head jobs |
| `compute` | future compute nodes | Pipeline task allocations |

Apply: `abc admin services nomad cli -- node-pool apply acl/node-pools/infra.hcl`  
Then add `node_pool = "infra"` to the relevant `client {}` stanzas and restart.

> `node_pool_config` inside a **namespace** HCL is **Enterprise-only** in Nomad OSS
> and will cause a 500 error if included. Omit it.

---

## Related documentation

| Document | Scope |
|----------|--------|
| `docs/abc-nodes-observability-and-operations.md` | Prometheus, Grafana, MinIO metrics, validation scripts |
| `docs/abc-nodes-testing-setup.md` | Stress/hyperfine workloads, MinIO bootstrap, dashboard sync |
