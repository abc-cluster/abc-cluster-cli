# Access Policy Plan — abc-nodes Enhanced Stack

Cluster: **aither** (`http://100.70.185.46:4646`, ACL enabled, single-node Nomad)
Last updated: 2026-04-20

This document defines access control for all services running on the abc-nodes
cluster: Nomad, MinIO/RustFS (S3), Vault, Loki, Prometheus, Grafana, ntfy,
Traefik, Alloy, and Wave.

---

## Namespace layout

| Namespace | Purpose | Job priority |
|-----------|---------|-------------|
| `default` | Unused (Nomad built-in) | — |
| `services` | All cluster infrastructure jobs — admin only | — |
| `applications` | **Planned** — shared platform application jobs (e.g. GVDS, BRIMS); not research pipelines | TBD (likely 75–80, below infra core, above group batch) |
| `su-mbhg-bioinformatics` | Research group jobs — high priority | 70 |
| `su-mbhg-hostgen` | Research group jobs — normal priority | 50 |

**`services` namespace** is write-protected: only tokens carrying the
`services-admin` policy (cluster-admin) can submit or manage jobs there.
Group tokens see nothing in `services`.

### Planned: `applications` namespace

We will add a dedicated Nomad namespace **`applications`** so long-lived **platform applications** (for example **GVDS**, **BRIMS**, and similar services) are isolated from:

- **`services`** — core abc-nodes floor (Traefik, MinIO, Vault, observability, auth helpers). Those jobs stay tightly controlled and minimal.
- **Research group namespaces** — Nextflow / pipeline workloads owned by a single group. Platform apps are cross-cutting and should not compete for the same ACL story as group members.

**Rollout plan (ACL + ops):**

1. **Namespace object** — Add `acl/namespaces/applications.hcl` (name `applications`, priority to be chosen after we confirm scheduler behaviour next to groups 70/50 and `services` job meta).
2. **Policies** — Introduce something analogous to `services-admin`, e.g. **`applications-admin`** (cluster-admin and/or a small platform team): write access only inside `applications`. Optionally **`applications-read`** for observers if we need shared dashboards or support accounts.
3. **Tokens** — Create dedicated Nomad ACL tokens for deploying and upgrading platform apps; **do not** reuse group pipeline tokens or member tokens. Research-group policies remain scoped to `su-mbhg-*` namespaces only.
4. **Nomad job specs** — New or migrated jobs for GVDS, BRIMS, etc. set `namespace = "applications"` (and use distinct job names so they never land in `default` or a group namespace by mistake).
5. **CLI contexts** — Operators maintain a separate `~/.abc` context (or `admin.whoami` / `admin.abc_nodes.nomad_namespace`) pointed at **`applications`** when running `abc job`, `abc admin services nomad cli`, or templated deploys for those apps.
6. **Ingress / auth** — Route public or VPN traffic via Traefik in `services` as today; application workloads in `applications` remain backend-only unless a job explicitly publishes ports through the mesh (same pattern as other app jobs).

This namespace is **not yet created** in the repo; the table row above marks intent. When the namespace and policies exist, extend the policy matrix in §1.1 and update `acl/README.md` directory layout.

---

## Case study: SU-MBHG Bioinformatics + Host Genetics

Two real research groups share the cluster:

| Group | Nomad namespace | Priority | S3 bucket |
|-------|----------------|----------|-----------|
| SU-MBHG Bioinformatics | `su-mbhg-bioinformatics` | 70 (high) | `su-mbhg-bioinformatics` |
| SU-MBHG Host Genetics  | `su-mbhg-hostgen`        | 50 (normal) | `su-mbhg-hostgen` |

Each group has three token/user tiers: **group-admin**, **submit** (pipeline
service account), and **member** (per-researcher).

---

## Role taxonomy

| Role | Who | Scope |
|------|-----|-------|
| **cluster-admin** | Lab operator, DevOps | Full control — all namespaces + services |
| **group-admin** | PI or designated postdoc per group | Full control within their group namespace + S3 bucket |
| **pipeline-submit** | nf-nomad service account (shared per group) | Submit and manage jobs in one namespace |
| **member** | Researcher / PhD student | Submit own jobs, access own S3 prefix only |
| **observer** | Collaborator, advisor | Read-only metrics and logs, no job management |

---

## 1. Nomad ACL

### 1.1 Policies

| Policy | Namespace | Job capabilities | Node | Agent |
|--------|-----------|-----------------|------|-------|
| `admin` | `*` write | all | write | write |
| `services-admin` | `services` write | all | write | write |
| `su-mbhg-bioinformatics-group-admin` | `su-mbhg-bioinformatics` write | all incl. scale | read | read |
| `su-mbhg-bioinformatics-submit` | `su-mbhg-bioinformatics` | submit/read/exec/lifecycle/logs | — | — |
| `su-mbhg-bioinformatics-member` | `su-mbhg-bioinformatics` | submit/read/exec/lifecycle/logs | — | — |
| `su-mbhg-hostgen-group-admin` | `su-mbhg-hostgen` write | all incl. scale | read | read |
| `su-mbhg-hostgen-submit` | `su-mbhg-hostgen` | submit/read/exec/lifecycle/logs | — | — |
| `su-mbhg-hostgen-member` | `su-mbhg-hostgen` | submit/read/exec/lifecycle/logs | — | — |
| `observer` | `*` | list/read/logs/fs | read | — |

**group-admin vs member**: Nomad OSS has no per-user job ownership enforcement.
Group-admin gets `policy = "write"` (cancel any job in the namespace) and
`scale-job`. Members rely on the honour system for cancellation.

### 1.2 Scheduler fairness

Priority + batch preemption enforces fairness between groups:

```
priority 90  cluster infrastructure             — never preempted
priority 80  auth / notifier (services ns)      — above user jobs
priority 70  su-mbhg-bioinformatics jobs        — preempts hostgen
priority 50  su-mbhg-hostgen jobs               — can be preempted by bio
priority 30  opportunistic / shared             — always preemptible
```

Enable (already applied on aither):
```bash
abc admin services nomad cli -- operator scheduler set-config \
  -preempt-batch-scheduler=true \
  -preempt-sysbatch-scheduler=true
```

### 1.3 Token naming convention

**Format: `su-mbhg-<group>_<username>`**  
The token **Name** equals the MinIO username. The Nomad token **SecretID** equals
the MinIO secret key. One credential pair per user.

| Nomad token Name | Nomad policy | MinIO user | MinIO policy |
|---|---|---|---|
| `su-mbhg-bioinformatics_submit` | `su-mbhg-bioinformatics-submit` | same | `su-mbhg-bioinformatics-group-admin` |
| `su-mbhg-bioinformatics_admin` | `su-mbhg-bioinformatics-group-admin` | same | `su-mbhg-bioinformatics-group-admin` |
| `su-mbhg-bioinformatics_alice` | `su-mbhg-bioinformatics-member` | same | `su-mbhg-bioinformatics-member` |
| `su-mbhg-hostgen_submit` | `su-mbhg-hostgen-submit` | same | `su-mbhg-hostgen-group-admin` |
| `su-mbhg-hostgen_admin` | `su-mbhg-hostgen-group-admin` | same | `su-mbhg-hostgen-group-admin` |
| `su-mbhg-hostgen_bob` | `su-mbhg-hostgen-member` | same | `su-mbhg-hostgen-member` |

Tokens are stored in `acl/tokens.env` (chmod 600, gitignored).

Create a new member token:
```bash
NOMAD_ADDR=http://100.70.185.46:4646 NOMAD_TOKEN=<mgmt-token> \
  abc admin services nomad cli -- acl token create \
    -name "su-mbhg-bioinformatics_carol" \
    -policy su-mbhg-bioinformatics-member \
    -type client
# Then create matching MinIO user with the new SecretID as the password
~/.abc/binaries/mc admin user add sunminio su-mbhg-bioinformatics_carol <SecretID>
~/.abc/binaries/mc admin policy attach sunminio su-mbhg-bioinformatics-member \
  --user su-mbhg-bioinformatics_carol
```

### 1.4 Apply (bootstrap)

```bash
# Prerequisites
export NOMAD_ADDR=http://100.70.185.46:4646
export NOMAD_TOKEN=<bootstrap-management-token>

# Namespaces (already applied on aither)
abc admin services nomad cli -- namespace apply acl/namespaces/su-mbhg-bioinformatics.hcl
abc admin services nomad cli -- namespace apply acl/namespaces/su-mbhg-hostgen.hcl

# Policies
abc admin services nomad cli -- acl policy apply \
  -description "Services namespace admin" services-admin acl/policies/services-admin.hcl
# ... (see acl/apply-su-mbhg.sh for the full script)
```

---

## 2. MinIO / S3

### 2.1 Bucket layout

| Bucket | Group | Member access | Group-admin access |
|--------|-------|---------------|--------------------|
| `su-mbhg-bioinformatics` | bioinformatics | own prefix (`alice/*`) only | full bucket |
| `su-mbhg-hostgen` | hostgen | own prefix (`bob/*`) only | full bucket |
| `su-mbhg-bioinformatics/shared/` | — | read-only | read+write |
| `su-mbhg-hostgen/shared/` | — | read-only | read+write |

### 2.2 Per-user prefix isolation

MinIO evaluates `${aws:username}` in policy conditions at request time.
The member policy restricts `s3:ListBucket` to their own prefix and
object operations to `<bucket>/<username>/*`.

Example — user `su-mbhg-bioinformatics_alice`:
- ✓ `ListBucket su-mbhg-bioinformatics` (prefix=`su-mbhg-bioinformatics_alice/`)
- ✓ `PutObject su-mbhg-bioinformatics/su-mbhg-bioinformatics_alice/results/`
- ✓ `GetObject su-mbhg-bioinformatics/shared/reference.fa`
- ✗ `GetObject su-mbhg-bioinformatics/su-mbhg-hostgen_bob/private.fastq` → 403
- ✗ `ListBucket su-mbhg-hostgen` → 403

The MinIO S3 username (`${aws:username}`) is the full token Name
(`su-mbhg-bioinformatics_alice`), so the per-user prefix is also the full
token Name. Adjust `s3:prefix` conditions in the IAM JSON if you prefer shorter
prefixes.

### 2.3 Token-as-S3-credential convention

For this Tailscale-isolated research cluster:
- **MinIO username** = Nomad token **Name** (e.g. `su-mbhg-bioinformatics_alice`)
- **MinIO secret key** = Nomad token **SecretID** (long random UUID)

Distribute the single SecretID to each user as both their Nomad token and
their S3 credential. If stronger separation is needed, generate independent
MinIO credentials and store both in Vault under
`secret/abc-nodes/users/<username>`.

### 2.4 IAM policy files

```
acl/minio-policies/
  su-mbhg-bioinformatics-group-admin.json  — s3:* on full bucket + tusd read
  su-mbhg-bioinformatics-member.json       — own prefix + shared/ read + tusd
  su-mbhg-hostgen-group-admin.json
  su-mbhg-hostgen-member.json
  pipeline-service-account.json            — full bucket for nf-nomad writes
```

Apply:
```bash
MC=~/.abc/binaries/mc
$MC alias set sunminio http://100.70.185.46:9000 minioadmin minioadmin --api s3v4
$MC admin policy create sunminio su-mbhg-bioinformatics-member \
  acl/minio-policies/su-mbhg-bioinformatics-member.json
```

---

## 3. abc-nodes-auth — Traefik ForwardAuth

The `abc-nodes-auth` Nomad job (in `services` namespace, port `9191`) authenticates
every upload request to tusd via Traefik's ForwardAuth middleware.

### 3.1 Architecture

```
Client → Traefik → abc-nodes-auth:9191/auth → Nomad /v1/acl/token/self
                                             ↓ X-Auth-User, X-Auth-Group, X-Auth-Namespace
                       → tusd (receives identity headers)
```

### 3.2 Identity headers returned

| Header | Value |
|---|---|
| `X-Auth-User` | Nomad token Name (= MinIO username) |
| `X-Auth-Group` | Group name derived from policy |
| `X-Auth-Namespace` | Nomad namespace for the group |

### 3.3 Policy map

The auth server maps Nomad policy names to group identity:

```python
POLICY_MAP = {
    "su-mbhg-bioinformatics-group-admin": ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-bioinformatics-submit":      ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-bioinformatics-member":      ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-hostgen-group-admin":        ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
    "su-mbhg-hostgen-submit":             ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
    "su-mbhg-hostgen-member":             ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
}
```

Add a new group by extending this map and redeploying the job.

### 3.4 Deploy

```bash
# Deploy (or redeploy after policy map changes)
abc admin services nomad cli -- job run \
  deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl

# Verify
curl -si http://100.70.185.46:9191/auth                                   # → 401
curl -si -H "X-Nomad-Token: <token>" http://100.70.185.46:9191/auth       # → 200
```

---

## 4. Traefik

| Route | Protection | Who |
|-------|-----------|-----|
| `/ping` | None | All |
| Dashboard (`:8888`) | BasicAuth | cluster-admin only |
| `/files/` (tusd) | Nomad ForwardAuth via abc-nodes-auth | Any valid group member |
| Other service routes | Tailscale network isolation | Node-level |

Traefik dynamic config for ForwardAuth:
`acl/tusd-auth/traefik-nomad-auth.yml` → `/etc/traefik/dynamic/` on the host.
Traefik hot-reloads it; no restart needed.

---

## 5. Vault

### 5.1 Status: uninitialized (WIP)

Vault is running (`abc-nodes-vault` in `services` namespace) but not yet
initialized or unsealed. Run the following once:

```bash
export VAULT_ADDR=http://100.70.185.46:8200
vault operator init       # save 5 unseal keys + root token securely
vault operator unseal     # run 3× with different key shares
vault operator unseal
vault operator unseal
vault secrets enable -path=secret kv-v2
abc config set admin.services.vault.http http://100.70.185.46:8200
abc cluster capabilities sync
```

### 5.2 Planned secret layout

```
secret/
  abc-nodes/
    nomad/
      management-token
      submit-tokens/
        su-mbhg-bioinformatics
        su-mbhg-hostgen
    minio/
      root
      users/
        su-mbhg-bioinformatics_alice   { access_key, secret_key }
        su-mbhg-bioinformatics_admin   { ... }
        su-mbhg-hostgen_bob            { ... }
    grafana/admin
    ntfy/admin
    traefik/dashboard
```

### 5.3 Health check note

Vault's `/sys/health` endpoint uses `?uninitcode=200&sealedcode=200` (not the
incorrect `uninitok`/`sealedok` params). This lets the Nomad deployment health
check pass regardless of Vault's initialization state.

---

## 6. Grafana

### 6.1 Access

| Grafana role | Who |
|-------------|-----|
| Admin | cluster-admin |
| Viewer | group-admin, member, observer |

Default admin: `admin` / `admin` (change immediately via Vault or `-var`).

### 6.2 Dashboard v2 features

Dashboard UID: `abc-nodes-overview` at `http://100.70.185.46:3000`

- **`$namespace` variable** — multi-select, sourced from Prometheus
  `nomad_nomad_job_summary_running` namespace label; drives all group panels
- **`$job` variable** — namespace-aware
- **Group Fairness row**: donut charts for CPU/memory/alloc share by namespace,
  queued-alloc pressure, preemption counter
- **Per-Group Detail row** (repeated per namespace): job state, per-task CPU %, memory RSS
- **Log Volume by Group**: Loki log rate by namespace
- **Stderr Rate by Group**: spike = failure in a specific group

---

## 7. Loki

Loki labels from Alloy: `alloc_id`, `task`, `stream`.  
The Nomad namespace is not yet a Loki label (Alloy scrapes by path, not namespace).

**Workaround**: filter by `task` label — Nextflow task names are unique per
pipeline and implicitly scoped to a namespace. A proper fix requires a Nomad API
sidecar in the Alloy pipeline to enrich logs with the namespace label.

---

## 8. Wave by Seqera (WIP)

Wave provides container build-and-cache capabilities for Nextflow pipelines.

| Job | Port | Status |
|-----|------|--------|
| `abc-nodes-redis` | 6379 | ✅ running (Wave dep) |
| `abc-nodes-postgres` | 5432 | ✅ running (Wave dep) |
| `abc-nodes-docker-registry` | 5000 | ✅ running (Wave dep) |
| `abc-nodes-wave` | 9090 | ⚠ needs ghcr.io auth |

**Blocking issue**: `ghcr.io/seqeralabs/wave:v1.33.2` returns 403 when pulled
without a GitHub PAT from the cluster's containerd runtime. Fix options:

A. Add `auth { username/password }` block with a GitHub PAT to `experimental/nomad/wave.nomad.hcl`.  
B. Configure `/etc/containerd/certs.d/ghcr.io/` credentials on `aither`.

Wave runs in `lite` mode: `MICRONAUT_ENVIRONMENTS=lite,rate-limit,redis,postgres,prometheus`.
Once deployed, configure Nextflow to use it:
```groovy
wave {
  enabled = true
  endpoint = 'http://100.70.185.46:9090'
}
```

---

## 9. ntfy — push notifications

| Topic | Publishers | Subscribers |
|-------|-----------|------------|
| `su-mbhg-bioinformatics-jobs` | job-notifier | bioinformatics members |
| `su-mbhg-hostgen-jobs` | job-notifier | hostgen members |
| `abc-admin` | cluster scripts | cluster-admin only |

When ntfy auth is enabled (not yet):
```bash
ntfy user add --role=user su-mbhg-bioinformatics_alice
ntfy access su-mbhg-bioinformatics_alice su-mbhg-bioinformatics-jobs ro
ntfy access ntfy-admin su-mbhg-bioinformatics-jobs rw
```

---

## 10. Implementation status

| Item | Status | Notes |
|------|--------|-------|
| Nomad ACL enabled | ✅ done | Bootstrap token in `~/.abc/config.yaml` |
| Namespaces: `services`, two group ns | ✅ done | |
| ACL policies (9 total) | ✅ done | |
| Tokens with `su-mbhg-<group>_<name>` naming | ✅ done | In `acl/tokens.env` |
| Batch preemption enabled | ✅ done | priority 70 preempts 50 |
| All service jobs in `services` namespace | ✅ done | 16 jobs |
| `abc-nodes-auth` ForwardAuth | ✅ done | Port 9191, tested |
| MinIO buckets + IAM policies | ✅ done | |
| MinIO users `su-mbhg-<group>_<name>` | ✅ done | Password = Nomad token SecretID |
| Vault health check fix (`uninitcode=200`) | ✅ done | |
| Wave deps (Redis, Postgres, Registry) | ✅ running | |
| Wave itself | ⚠ WIP | Blocked on ghcr.io auth |
| Vault init + unseal | ⚠ WIP | Job running, not yet initialized |
| Traefik ForwardAuth wired to tusd | ⚠ WIP | `traefik-nomad-auth.yml` needs placement |
| ntfy auth (per-group topics) | ⚠ WIP | Design done |
| Grafana Viewer accounts per group | ⚠ WIP | Design done |
| Vault AppRole / Workload Identity | ⚠ WIP | Design done |
| Alloy namespace label forwarding | ⚠ WIP | Needs Nomad API sidecar |

---

## 11. Quick reference — who can do what

| Action | cluster-admin | group-admin | submit | member | observer |
|--------|:---:|:---:|:---:|:---:|:---:|
| Manage `services` namespace jobs | ✓ | — | — | — | — |
| Submit jobs (own namespace) | ✓ | ✓ | ✓ | ✓ | — |
| Cancel any job in own namespace | ✓ | ✓ | — | — | — |
| Read job status (own namespace) | ✓ | ✓ | ✓ | ✓ | ✓ (all) |
| Tail Nomad logs | ✓ | ✓ (own) | ✓ (own) | ✓ (own) | ✓ (all) |
| `abc data ls` own S3 prefix | ✓ | ✓ (full bucket) | ✓ (full bucket) | ✓ (own prefix) | — |
| `abc data ls` other group | ✓ | — | — | — | — |
| Upload via tusd | ✓ | ✓ | ✓ | ✓ | — |
| Grafana dashboards | ✓ (edit) | ✓ (view) | — | ✓ (view) | ✓ (view) |
| Vault secrets | ✓ (all) | group creds | — | own creds | — |
| MinIO IAM (create users/policies) | ✓ | — | — | — | — |
