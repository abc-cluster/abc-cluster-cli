# abc-nodes Services Reference

> **Scope:** This document covers the full `abc-nodes` service stack — the set of Nomad jobs that make up the *abc-enhanced* deployment tier. The enhanced tier extends the base tier (MinIO + tusd) with a complete observability stack, secrets management, push notifications, a database platform, and a container-build service.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Service Inventory](#service-inventory)
3. [Storage & Data Layer](#storage--data-layer)
   - [MinIO](#minio)
   - [Supabase](#supabase)
   - [Vault](#vault)
   - [RustFS](#rustfs)
4. [Upload & Transfer Layer](#upload--transfer-layer)
   - [tusd](#tusd)
   - [Uppy Dashboard](#uppy-dashboard)
5. [Observability Stack](#observability-stack)
   - [Grafana Alloy](#grafana-alloy)
   - [Prometheus](#prometheus)
   - [Loki](#loki)
   - [Grafana](#grafana)
6. [Notifications](#notifications)
   - [ntfy](#ntfy)
   - [Job Notifier](#job-notifier)
7. [Networking & Access Control](#networking--access-control)
   - [Traefik](#traefik)
   - [abc-nodes-auth (ForwardAuth)](#abc-nodes-auth-forwardauth)
8. [Container Build Service](#container-build-service)
   - [Wave](#wave)
   - [Docker Registry](#docker-registry)
9. [Secrets Management](#secrets-management)
   - [Nomad Variables](#nomad-variables)
   - [Vault KV](#vault-kv)
10. [Deployment Bootstrap](#deployment-bootstrap)
11. [Port Reference](#port-reference)
12. [Traefik Route Reference](#traefik-route-reference)

---

## Architecture Overview

```
                        Tailscale VPN (100.70.x.x)
                                │
                    ┌───────────▼────────────┐
                    │   Traefik  :80 / :8888 │  reverse proxy + ForwardAuth
                    └──────┬────────┬────────┘
                           │        │
          ┌────────────────┤        ├─────────────────────┐
          │                │        │                     │
    ┌─────▼──────┐  ┌──────▼───┐  ┌▼──────────┐  ┌──────▼─────┐
    │  tusd :8080│  │ Uppy:8085│  │Grafana:3000│  │ ntfy :8088 │
    │  (uploads) │  │(web UI)  │  │(dashboards)│  │(push notif)│
    └─────┬──────┘  └──────────┘  └─────┬──────┘  └──────┬─────┘
          │  S3 writes                  │ queries         │ attachments
    ┌─────▼──────┐              ┌───────▼──────┐          │
    │ MinIO :9000│◄─────────────│ Prometheus   │◄─────────┘
    │ (S3 store) │  loki chunks │    :9090     │ scrape
    │ Console:9001│  & index    └───────┬──────┘
    └────────────┘              ┌───────▼──────┐
                                │ Loki  :3100  │
                                │ (log store)  │
                                └───────┬──────┘
                                        │ logs from
                                ┌───────▼──────┐
                                │ Alloy (system│
                                │ job on host) │
                                └──────────────┘

    ┌─────────────────────────────────────────────────────┐
    │ Supabase (optional — stopped by default)            │
    │  db:5432  rest:3001  auth:9999  meta:8081           │
    │  studio:3002  kong:8000                             │
    └─────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────┐
    │ Vault :8200   (secrets KV v2)                       │
    │ abc-nodes-auth :9191  (Nomad ACL ForwardAuth)       │
    │ Wave :9091   (container build — stopped by default) │
    └─────────────────────────────────────────────────────┘
```

**Deployment model:** All jobs run inside HashiCorp Nomad on a single node (`aither`, Tailscale IP `100.70.185.46`). Containers use the `containerd-driver` with `mode = "bridge"` networking; host processes use `raw_exec` with `mode = "host"`.

---

## Service Inventory

| Job Name | Type | Image | Ports | Status |
|---|---|---|---|---|
| `abc-nodes-minio` | service | minio/minio | 9000 (S3), 9001 (console) | always on |
| `abc-nodes-supabase` | service | supabase/* (6 containers) | 5432, 3001, 9999, 8081, 3002, 8000 | opt-in |
| `abc-nodes-vault` | service | hashicorp/vault | 8200 | always on |
| `abc-nodes-rustfs` | service | rustfs/rustfs | bridge | always on |
| `abc-nodes-tusd` | service | tusproject/tusd | 8080 | always on |
| `abc-nodes-uppy` | service | nginx (static) | 8085 | always on |
| `abc-nodes-alloy` | system | grafana/alloy | host | always on |
| `abc-nodes-prometheus` | service | prom/prometheus | 9090 | always on |
| `abc-nodes-loki` | service | grafana/loki | 3100 | always on |
| `abc-nodes-grafana` | service | grafana/grafana | 3000 | always on |
| `abc-nodes-ntfy` | service | binwiederhier/ntfy | 8088 | always on |
| `abc-nodes-job-notifier` | service | raw_exec (bash+jq) | — | always on |
| `abc-nodes-traefik` | service | raw_exec (traefik bin) | 80, 8888 | always on |
| `abc-nodes-auth` | service | raw_exec (python) | 9191 | always on |
| `abc-nodes-docker-registry` | service | registry:2 | 5000 | always on |
| `abc-nodes-wave` | service | seqera/wave | 9091 | opt-in |

---

## Storage & Data Layer

### MinIO

**Job:** `abc-nodes-minio`  
**Image:** `minio/minio:RELEASE.2024-12-18T13-15-44Z`  
**Ports:** `9000` (S3 API), `9001` (web console)

MinIO provides S3-compatible object storage for every service on the floor that needs durable blob storage: Loki stores log chunks and index data here, ntfy stores notification attachments here, Wave caches container layers here, and tusd writes upload parts here.

**Data persistence:** Data is stored at `/opt/nomad/scratch/minio-data` on the host, mounted into the container at `/scratch/minio-data`. This path survives Nomad restarts and allocation replacements.

**Credentials:** Root user and password are never hardcoded. They are read at runtime from a Nomad Variable:

```
Path (namespace services): nomad/jobs/abc-nodes-minio
Keys: minio_root_user, minio_root_password
```

Store or rotate:
```bash
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-minio \
  minio_root_user="minio-admin" \
  minio_root_password="<generated-password>"
```

> **Note:** Because Nomad ACL prevents a task from reading another job's variables, services that consume MinIO credentials (Loki, ntfy) keep a *copy* of the creds under their own variable path (`nomad/jobs/abc-nodes-loki`, `nomad/jobs/abc-nodes-ntfy`). The `store-cluster-secrets.sh` bootstrap script keeps all three in sync.

**Buckets used by other services:**

| Bucket | Consumer |
|--------|----------|
| `loki` | Loki — chunks + TSDB index |
| `ntfy` | ntfy — notification attachments |
| `tusd` | tusd — TUS upload parts |
| `wave` | Wave — container layer cache |

**Research-group storage automation:**

Use `deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh` to provision namespace buckets, user-prefix IAM policies, group-admin bucket policies, and a cluster-admin all-data policy. The script also persists generated IAM credentials to a local gitignored file, Nomad Variables, and optionally Vault KV v2.

**Traefik routes:** `minio.aither` → S3 API, `minio-console.aither` → web console.

---

### Supabase

**Job:** `abc-nodes-supabase` (6 task groups)  
**Status:** Opt-in (stopped by default — run `job run supabase.nomad.hcl` to start)

Supabase is a PostgreSQL-based backend platform. It replaces the standalone `abc-nodes-postgres` job and adds a REST API, authentication, schema introspection, a web UI, and an API gateway on top of PostgreSQL.

| Group | Image | Port | Role |
|-------|-------|------|------|
| `db` | `supabase/postgres:15.8.1.060` | 5432 | PostgreSQL + Supabase extensions |
| `rest` | `postgrest/postgrest:v12.2.8` | 3001 | Auto-generated REST API over the DB |
| `auth` | `supabase/gotrue:v2.170.0` | 9999 | JWT-based authentication (GoTrue) |
| `meta` | `supabase/postgres-meta:v0.84.2` | 8081 | Schema introspection API |
| `studio` | `supabase/studio:20250317-6955350` | 3002 | Supabase Studio web UI |
| `kong` | `kong:2.8.1` | 8000 | API gateway (routes auth/rest/meta) |

**Why separate groups?** The `containerd-driver` on this cluster does not share network namespaces between tasks within the same group. Each group gets its own bridge network with a static host-port mapping. Inter-service communication uses the host Tailscale IP (`100.70.185.46`) rather than `127.0.0.1`.

**Data persistence:** PostgreSQL data is bind-mounted from `/opt/nomad/scratch/supabase-data` → `/var/lib/postgresql/data`. The `supabase/postgres` image ignores the `PGDATA` env var; only a direct bind mount to the canonical path works.

**Credentials:** All secrets are stored in Nomad Variables and injected at runtime:

```
Path (namespace services): nomad/jobs/abc-nodes-supabase
Keys: postgres_password, jwt_secret, anon_key, service_role_key, wave_db_password
```

Generate fresh secrets (including JWT tokens via HMAC-SHA256):
```bash
bash deployments/abc-nodes/scripts/init-supabase-secrets.sh
```

**Wave database:** On first init, the `db-prep` prestart task writes `/docker-entrypoint-initdb.d/99-wave.sql` to create the `wave` role and database with the password from `wave_db_password`.

**Traefik routes:** `supabase.aither` → Kong (API gateway), `supabase-studio.aither` → Studio UI.

---

### Vault

**Job:** `abc-nodes-vault`  
**Image:** `hashicorp/vault:1.18.3` (raw_exec binary, not container)  
**Port:** `8200` (API + UI)

HashiCorp Vault provides secrets management with KV v2 storage. It runs as a raw_exec process so it can use host network and access `/opt/nomad/vault/data` directly for Raft integrated storage.

**Data persistence:** Raft data at `/opt/nomad/vault/data` — survives restarts.

**First-run initialization:**
```bash
bash deployments/abc-nodes/scripts/init-vault.sh
```
This script:
1. Detects whether Vault is already initialized
2. If not, calls `POST /v1/sys/init` (5 shares, threshold 3)
3. Unseals with the first 3 key shares
4. Saves keys + root token to `acl/vault-keys.env` (chmod 600)

> **Important:** Vault is sealed on every restart. After any Nomad reschedule, run the unseal steps (3× `vault operator unseal`) or re-run `init-vault.sh` which handles the unseal path automatically.

**KV v2 setup:**
```bash
export VAULT_ADDR=http://100.70.185.46:8200
export VAULT_TOKEN=<root-token>
vault secrets enable -path=secret kv-v2
```

**abc CLI integration:**
```bash
abc config set admin.services.vault.http "http://100.70.185.46:8200"
abc cluster capabilities sync
# Then store/retrieve secrets:
abc secrets set my-key "value" --backend vault
abc secrets ref my-key --backend vault
```

**Traefik route:** `vault.aither` → Vault UI.

---

### RustFS

**Job:** `abc-nodes-rustfs`  
**Image:** `rustfs/rustfs:latest`

RustFS is a lightweight S3-compatible object store written in Rust. It runs as an alternative/supplementary storage backend. On this cluster it operates with container-local `/data` storage (no host persistence configured). It is primarily available as a failover or test target.

**Traefik route:** `rustfs.aither`

---

## Upload & Transfer Layer

### tusd

**Job:** `abc-nodes-tusd`  
**Image:** `tusproject/tusd:v2.4.0`  
**Port:** `8080`

tusd implements the [TUS resumable upload protocol](https://tus.io). It receives large file uploads from clients (Uppy dashboard or the `abc` CLI) and writes them to MinIO via the S3 backend. Uploads are resumable — if a connection drops mid-transfer, the client can resume from the last byte offset without re-uploading.

**Authentication:** tusd sits behind Traefik's `nomad-auth` ForwardAuth middleware. Every upload request must carry a valid Nomad ACL token in `X-Nomad-Token` or `Authorization: Bearer <token>`. The abc-nodes-auth sidecar validates the token and returns `X-Auth-User`, `X-Auth-Group`, and `X-Auth-Namespace` headers, which appear in tusd access logs.

**Traefik route:** `tusd.aither` (with `nomad-auth` middleware enforced).

---

### Uppy Dashboard

**Job:** `abc-nodes-uppy`  
**Image:** `nginx:1.27-alpine` (serves static HTML)  
**Port:** `8085`

A pre-built Uppy web UI served via nginx. Users on the Tailscale network can open it in a browser to drag-and-drop files for upload to tusd. No server-side state — purely a static client.

**Traefik route:** `uppy.aither`

---

## Observability Stack

The abc-enhanced tier ships a complete three-tier observability stack: **Alloy** (agent) → **Prometheus + Loki** (backends) → **Grafana** (dashboards).

### Grafana Alloy

**Job:** `abc-nodes-alloy` (Nomad system job — runs on every node)  
**Image:** `grafana/alloy:1.15.1`  
**Network:** `host` mode (raw_exec-style access to host paths and ports)

Alloy is a telemetry pipeline agent. On each node it:

1. **Tails Nomad allocation log files** from `/opt/nomad/alloc/*/alloc/logs/*.{stdout,stderr}.[0-9]` and ships them to Loki, attaching labels derived from the directory path (job ID, task name, allocation ID).
2. **Scrapes Prometheus metrics** from Nomad's own metrics endpoint.
3. **Exposes its own metrics** on port `12345` for Prometheus to scrape.

Because it's a system job, it automatically deploys to every Nomad node added to the cluster without extra configuration.

**Traefik route:** `grafana-alloy.aither` → Alloy UI (pipeline visualization).

---

### Prometheus

**Job:** `abc-nodes-prometheus`  
**Image:** `prom/prometheus:v2.54.1`  
**Port:** `9090`

Prometheus scrapes time-series metrics from Nomad (via Alloy), Alloy itself, and any other services that expose a `/metrics` endpoint. The TSDB is stored ephemerally inside the container at `/prometheus` — metrics history is lost on reschedule, which is acceptable for a lab cluster.

**Traefik route:** `prometheus.aither`

---

### Loki

**Job:** `abc-nodes-loki`  
**Image:** `grafana/loki:3.3.2`  
**Port:** `3100`

Loki is a log aggregation system. It receives log streams from Alloy and makes them queryable via LogQL (used by Grafana dashboards). Log chunks and the TSDB index are stored durably in MinIO (bucket `loki`), so logs survive Loki restarts.

**Configuration:** Uses single-process mode with an in-memory ring (suitable for single-node deployment). The storage backend is configured at runtime via a Nomad template that pulls MinIO credentials from `nomad/jobs/abc-nodes-loki`.

**MinIO credentials (Nomad Variable):**
```
Path (namespace services): nomad/jobs/abc-nodes-loki
Keys: minio_access_key, minio_secret_key
```

**Traefik route:** `loki.aither`

---

### Grafana

**Job:** `abc-nodes-grafana`  
**Image:** `grafana/grafana:11.4.0`  
**Port:** `3000`

Grafana provides the dashboards UI. It is pre-provisioned with two data sources (Prometheus and Loki) and two dashboards:

| Dashboard | UID | Description |
|-----------|-----|-------------|
| Nomad allocation logs | `abc-nodes-nomad-loki-logs` | All allocation stdout/stderr via Loki |
| Pipeline Jobs Monitor | `abc-nodes-pipeline-monitor` | Per-namespace Loki panels for `su-mbhg-bioinformatics`, `su-mbhg-hostgen`, plus job-notifier send events |

**Data persistence:** Grafana state (SQLite DB, users, plugins, sessions) is stored at `/opt/nomad/scratch/grafana-data` on the host. A `grafana-init` prestart task runs `chown -R 472:472` on that directory so the Grafana container (which runs as UID 472) can write to it.

**Admin password (Nomad Variable):**
```
Path (namespace services): nomad/jobs/abc-nodes-grafana
Keys: admin_password
```

**Traefik route:** `grafana.aither`

---

## Notifications

### ntfy

**Job:** `abc-nodes-ntfy`  
**Image:** `binwiederhier/ntfy:v2.11.0`  
**Port:** `8088`

ntfy is a simple pub/sub push notification server. It allows the cluster (via the job-notifier) and external tools to push notifications to topics that users subscribe to via the ntfy mobile/desktop app or web UI.

Notification attachments (files included in messages) are stored in MinIO (bucket `ntfy`) rather than on local disk, making them durable.

**MinIO credentials (Nomad Variable):**
```
Path (namespace services): nomad/jobs/abc-nodes-ntfy
Keys: minio_access_key, minio_secret_key
```

**Primary topic:** `abc-jobs` — receives Nomad job completion/failure notifications.

**Traefik route:** `ntfy.aither`

---

### Job Notifier

**Job:** `abc-nodes-job-notifier`  
**Driver:** `raw_exec` (bash + `jq` on host)

The job notifier streams the Nomad event API (`GET /v1/event/stream?topic=Allocation`) and posts to ntfy whenever an allocation reaches a terminal state.

| Allocation status | ntfy priority | Message |
|---|---|---|
| `complete` | 3 (default) | `<job> (<namespace>) alloc <id> finished successfully.` |
| `failed` | 4 (high) | `<job> (<namespace>) alloc <id> FAILED. Check Nomad UI.` |
| `lost` | 4 (high) | `<job> (<namespace>) alloc <id> was lost (node issue?).` |

The notifier skips events from its own job (`abc-nodes-job-notifier`) to prevent feedback loops.

**Nomad token (Nomad Variable):**
```
Path (namespace services): nomad/jobs/abc-nodes-job-notifier
Keys: nomad_token
```

The token is injected via a template at runtime — it is never committed to the job spec or git history.

---

## Networking & Access Control

### Traefik

**Job:** `abc-nodes-traefik`  
**Driver:** `raw_exec` (downloads the Traefik binary as an artifact)  
**Ports:** `8081` (HTTP entry), `8888` (Traefik dashboard)

Traefik runs as an internal service router and dashboard in the current setup. It no longer owns external LAN port `80` (that role moved to Caddy), so it listens on `8081` and `8888`.

Routes are defined in a Nomad template (`routes.yml`) that is regenerated automatically whenever a registered service address changes. Backend addresses are discovered via the Nomad service catalog (`nomadService` template function).

**Middleware: `nomad-auth`**  
A ForwardAuth middleware that calls `http://127.0.0.1:9191/auth` before forwarding requests to protected services. Currently applied to the `tusd` router. See [abc-nodes-auth](#abc-nodes-auth-forwardauth) below.

**Dashboard:** `http://100.70.185.46:8888/dashboard/` — shows all live routers, services, and middleware state.

---

### Caddy LAN Gateway (current ingress)

**Config:** `deployments/abc-nodes/caddy/Caddyfile.lan`  
**Service:** `systemd` unit `caddy.service` on `sun-aither`  
**Bind:** `146.232.174.77:80` and `146.232.174.77:443`

Caddy is the user-facing LAN ingress for `aither.mb.sun.ac.za`. It proxies path-based routes to internal service backends (currently on `100.70.185.46`).

Current behavior:

- **HTTP remains primary**: `http://aither.mb.sun.ac.za` serves the portal and routes directly.
- **HTTPS is configured but not enforced**: redirects are intentionally disabled for now (`auto_https disable_redirects`) until public DNS is in place.
- **Public cert is pending DNS**: ACME issuance fails with `NXDOMAIN` for `aither.mb.sun.ac.za`; once public A/AAAA records exist and propagate, reload Caddy to retry issuance.
- **Path groups in portal**:
  - Cluster users: Nomad (`/ui/settings/tokens`), MinIO Console (`/minio-console/`), Uppy (`/uppy/`)
  - Cluster admins: Prometheus, Loki, Vault, ntfy, Grafana

---

### abc-nodes-auth (ForwardAuth)

**Job:** `abc-nodes-auth`  
**Driver:** `raw_exec` (Python HTTP server)  
**Port:** `9191` (localhost only)

A lightweight Python HTTP server that validates Nomad ACL tokens. When Traefik receives a request on a protected route, it calls `GET http://127.0.0.1:9191/auth` with the original request headers forwarded. The auth server:

1. Reads `X-Nomad-Token` or `Authorization: Bearer <token>` from the request
2. Validates the token against the Nomad API
3. Returns `200 OK` with `X-Auth-User`, `X-Auth-Group`, `X-Auth-Namespace` headers on success
4. Returns `401 Unauthorized` on failure, causing Traefik to reject the upstream request

This ensures only users with a valid Nomad ACL token can initiate TUS uploads.

---

## Container Build Service

### Wave

**Job:** `abc-nodes-wave`  
**Image:** `seqera/wave:v1.33.2` (private AWS ECR — requires registry credentials)  
**Port:** `9091`  
**Status:** Opt-in (stopped by default)

Wave is Seqera's container augmentation service used by Nextflow pipelines. In "lite mode" (single JVM process) it can:

- Pull and cache container images from public registries
- Build containers on-demand from `conda` environment specs
- Store cached layers in MinIO

**Dependencies:** Wave requires a running PostgreSQL database and Redis. When using Supabase, the `wave` database and role are pre-created by Supabase's init SQL (`99-wave.sql`). When using standalone PostgreSQL, the `abc-nodes-postgres` job must be running.

**Wave DB password (Nomad Variable):**
```
Path (namespace services): nomad/jobs/abc-nodes-wave
Keys: wave_db_password
```

This password is also written to `nomad/jobs/abc-nodes-supabase` as `wave_db_password` so the Supabase init SQL can create the role with the correct password.

**Traefik route:** `wave.aither`

---

### Docker Registry

**Job:** `abc-nodes-docker-registry`  
**Image:** `registry:2`  
**Port:** `5000`

A local OCI-compliant container registry. Used primarily as a pull-through cache for Wave so that frequently-used images are served from the local network rather than pulled from the internet on every pipeline run. Configured as insecure HTTP (suitable for Tailscale-only access).

---

## Secrets Management

The cluster uses two complementary systems for secrets.

### Nomad Variables

Nomad Variables store per-job runtime secrets (passwords, API tokens, JWT keys). They are the primary secret mechanism for container environment variables.

**ACL scope:** Each task can read variables at `nomad/jobs/<its-own-job-id>` in its own namespace by default. Cross-job reads require an explicit ACL policy — to avoid this complexity, shared secrets (e.g., MinIO credentials consumed by Loki and ntfy) are copied to each consuming job's own variable path.

**All variable paths used on this cluster:**

| Path | Namespace | Keys | Consumer |
|------|-----------|------|----------|
| `nomad/jobs/abc-nodes-minio` | services | `minio_root_user`, `minio_root_password` | MinIO itself |
| `nomad/jobs/abc-nodes-loki` | services | `minio_access_key`, `minio_secret_key` | Loki → MinIO |
| `nomad/jobs/abc-nodes-ntfy` | services | `minio_access_key`, `minio_secret_key` | ntfy → MinIO |
| `nomad/jobs/abc-nodes-grafana` | services | `admin_password` | Grafana admin login |
| `nomad/jobs/abc-nodes-job-notifier` | services | `nomad_token` | Job notifier → Nomad API |
| `nomad/jobs/abc-nodes-supabase` | services | `postgres_password`, `jwt_secret`, `anon_key`, `service_role_key`, `wave_db_password` | Supabase stack |
| `nomad/jobs/abc-nodes-wave` | services | `wave_db_password` | Wave → PostgreSQL |
| `nomad/jobs/abc-nodes-minio-iam/<principal>` | services | `access_key`, `secret_key`, `role`, `scope`, `bucket` | MinIO IAM principals for namespace buckets |

**Bootstrap (store all at once):**
```bash
export NOMAD_TOKEN=<management-token>
bash deployments/abc-nodes/scripts/store-cluster-secrets.sh
bash deployments/abc-nodes/scripts/init-supabase-secrets.sh  # generates JWTs
```

---

### Vault KV

HashiCorp Vault (KV v2 at path `secret/`) provides a more general secrets store for cases where Nomad Variables are insufficient: long-term secret rotation, programmatic access from job code, or secrets that are not tied to a specific Nomad job.

**abc CLI integration:**
```bash
# Store a secret
abc secrets set pipeline/db-password "s3cr3t" --backend vault

# Reference in a Nextflow pipeline (returns a placeholder that resolves at runtime)
abc secrets ref pipeline/db-password --backend vault
```

Vault is sealed on restart. Keep `acl/vault-keys.env` (generated by `init-vault.sh`) in a safe location — it contains the unseal keys and root token.

---

### MinIO IAM Rotation

Research namespace IAM credentials are rotated by re-running:

- `deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh`

with new secret inputs (for example `USER_PASSWORD_PREFIX` and/or `CLUSTER_ADMIN_IAM_PASS`).

Rotation updates all configured sinks in one pass:

- MinIO IAM users + policy attachments
- Local credential snapshot: `deployments/abc-nodes/acl/minio-credentials.env`
- Nomad Variables: `nomad/jobs/abc-nodes-minio-iam/<principal>`
- Vault KV (optional): `secret/abc-nodes/minio-iam/<principal>`

Example:

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

---

## Deployment Bootstrap

Full first-run sequence after a fresh cluster node is provisioned:

```bash
# 1. Store all Nomad Variable secrets
export NOMAD_TOKEN=<management-token>
bash deployments/abc-nodes/scripts/store-cluster-secrets.sh

# 2. Generate and store Supabase JWT secrets
bash deployments/abc-nodes/scripts/init-supabase-secrets.sh

# 3. Deploy core services (order matters: minio before loki/ntfy)
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/loki.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/ntfy.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/grafana.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/job-notifier.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/traefik.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/vault.nomad.hcl

# 4. Initialize and unseal Vault
bash deployments/abc-nodes/scripts/init-vault.sh

# 5. (Optional) Deploy Supabase
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/supabase.nomad.hcl

# 6. Update mc alias with rotated MinIO credentials
mc alias set sunminio http://100.70.185.46:9000 minio-admin <minio_root_password>
```

---

## Port Reference

| Port | Service | Protocol |
|------|---------|----------|
| 80 | Traefik HTTP entry | HTTP |
| 3000 | Grafana | HTTP |
| 3001 | Supabase PostgREST | HTTP |
| 3002 | Supabase Studio | HTTP |
| 3100 | Loki | HTTP |
| 5000 | Docker Registry | HTTP (insecure) |
| 5432 | Supabase PostgreSQL | TCP |
| 8000 | Supabase Kong gateway | HTTP |
| 8080 | tusd | HTTP (TUS) |
| 8081 | Supabase postgres-meta | HTTP |
| 8085 | Uppy Dashboard | HTTP |
| 8088 | ntfy | HTTP |
| 8200 | Vault | HTTP |
| 8888 | Traefik dashboard | HTTP |
| 9000 | MinIO S3 API | HTTP |
| 9001 | MinIO Console | HTTP |
| 9090 | Prometheus | HTTP |
| 9091 | Wave | HTTP |
| 9099 | Supabase GoTrue auth | HTTP |
| 9191 | abc-nodes-auth (ForwardAuth) | HTTP (localhost only) |
| 12345 | Grafana Alloy | HTTP |

---

## Traefik Route Reference

All routes resolve via `*.aither` hostnames (configured in Tailscale DNS or `/etc/hosts` on client machines pointing to `100.70.185.46`).

| Hostname | Backend | Auth |
|---|---|---|
| `grafana.aither` | abc-nodes-grafana :3000 | none |
| `grafana-alloy.aither` | abc-nodes-alloy :12345 | none |
| `loki.aither` | abc-nodes-loki :3100 | none |
| `minio.aither` | abc-nodes-minio S3 :9000 | none |
| `minio-console.aither` | abc-nodes-minio console :9001 | none |
| `ntfy.aither` | abc-nodes-ntfy :8088 | none |
| `prometheus.aither` | abc-nodes-prometheus :9090 | none |
| `rustfs.aither` | abc-nodes-rustfs | none |
| `tusd.aither` | abc-nodes-tusd :8080 | **nomad-auth** (Nomad ACL token required) |
| `uppy.aither` | abc-nodes-uppy :8085 | none |
| `vault.aither` | abc-nodes-vault :8200 | none |
| `wave.aither` | abc-nodes-wave :9091 | none |
| `supabase.aither` | Supabase Kong :8000 | none |
| `supabase-studio.aither` | Supabase Studio :3002 | none |
| `faasd.aither` | faasd :8089 (on hold) | none |
