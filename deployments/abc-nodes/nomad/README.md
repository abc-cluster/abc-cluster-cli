# Nomad jobs — `abc-nodes` services namespace

All jobs in this directory run in the **`services`** Nomad namespace. That namespace is
write-accessible only to tokens carrying the `services-admin` policy (i.e. cluster-admin
tokens). Research-group tokens cannot see or interact with these jobs.

Jobs use the community [**containerd-driver**](https://github.com/Roblox/nomad-driver-containerd)
(`driver = "containerd-driver"`) or `raw_exec` for lightweight shell processes.
The Docker driver is **not** required and is not enabled on this cluster.

---

## Prerequisites

### 1. Nomad client plugins

- **containerd-driver** installed and enabled; **containerd** running.
- `containerd_runtime = "io.containerd.runc.v2"` in client plugin config.
- For `network { mode = "bridge" }`: **`bridge` kernel module** loaded + **CNI plugins** at `cni_path`.
  - Check: `journalctl -u nomad | grep bridge`
  - Fix: `sudo modprobe bridge && echo bridge | sudo tee /etc/modules-load.d/nomad-bridge.conf && sudo systemctl restart nomad`
  - Current cluster: all jobs use `mode = "host"` — bridge module not required right now.

### 2. Host volumes

Only the `scratch` volume (`/opt/nomad/scratch`) is configured on `aither`.
PostgreSQL (optional Wave stack) writes to `/opt/nomad/scratch/wave-postgres/pgdata` when deployed.

For production, add dedicated host volumes to the Nomad client config and update
the relevant job files:

```hcl
# /etc/nomad.d/client.hcl  (example additional volumes)
host_volume "wave-postgres" {
  path      = "/var/lib/abc-nodes/postgres"
  read_only = false
}
```

### 3. abc context

Active context must be `aither` (or whichever context points to
`http://100.70.185.46:4646`). The management token is stored in
`~/.abc/config.yaml` under `contexts.min.admin.services.nomad.nomad_token`.

```bash
abc context use aither
abc admin services nomad cli -- namespace list   # should show 'services'
```

---

## Cluster layout (as deployed)

```
Nomad cluster:  http://100.70.185.46:4646   (single node: aither)
MinIO S3 API:   http://100.70.185.46:9000
Grafana:        http://100.70.185.46:3000
Prometheus:     http://100.70.185.46:9090
Loki:           http://100.70.185.46:3100
ntfy:           http://100.70.185.46:8088
faasd:          http://100.70.185.46:8089
Traefik HTTP:   http://100.70.185.46:80   (dashboard: :8888)
tusd:           http://100.70.185.46:8080/files/
Docker Registry:http://100.70.185.46:5000
```

Opt-in **Vault**, **Supabase**, and **Wave** jobs (not in this layout by default): see **`../experimental/README.md`**.

---

## Job inventory

| Job file | Job name | Port(s) | Status | Notes |
|---|---|---|---|---|
| `traefik.nomad.hcl` | `abc-nodes-traefik` | 80, 8888 | ✅ running | raw_exec, reverse proxy |
| `minio.nomad.hcl` | `abc-nodes-minio` | 9000, 9001 | ✅ running | containerd |
| `rustfs.nomad.hcl` | `abc-nodes-rustfs` | 9900, 9901 | ✅ running | containerd |
| `loki.nomad.hcl` | `abc-nodes-loki` | 3100 | ✅ running | containerd |
| `prometheus.nomad.hcl` | `abc-nodes-prometheus` | 9090 | ✅ running | containerd |
| `grafana.nomad.hcl` | `abc-nodes-grafana` | 3000 | ✅ running | containerd |
| `ntfy.nomad.hcl` | `abc-nodes-ntfy` | 8088 | ✅ running | containerd |
| `faasd.nomad.hcl` | `abc-nodes-faasd` | 8089 | ✅ planned | containerd |
| `alloy.nomad.hcl` | `abc-nodes-alloy` | 12345 | ✅ running | raw_exec, system job |
| `tusd.nomad.hcl` | `abc-nodes-tusd` | 8080 | ✅ running | containerd |
| `uppy.nomad.hcl` | `abc-nodes-uppy` | 8090 | ✅ running | containerd |
| `job-notifier.nomad.hcl` | `abc-nodes-job-notifier` | — | ✅ running | raw_exec |
| `abc-nodes-auth.nomad.hcl` | `abc-nodes-auth` | 9191 | ✅ running | exec, Traefik ForwardAuth |
| `redis.nomad.hcl` | `abc-nodes-redis` | 6379 | ✅ running | containerd, optional Wave dep |
| `postgres.nomad.hcl` | `abc-nodes-postgres` | 5432 | ✅ running | containerd, optional Wave dep |
| `docker-registry.nomad.hcl` | `abc-nodes-docker-registry` | 5000 | ✅ running | containerd |

Experimental (Vault / Supabase / Wave): **`../experimental/nomad/*.nomad.hcl`** — see **`../experimental/README.md`**.

---

## Deployment order

```bash
# From the abc-cluster-cli repo root, with aither context active:

# 1. Reverse proxy (no deps)
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/traefik.nomad.hcl

# 2. Storage
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/rustfs.nomad.hcl

# 3. Observability stack
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/prometheus.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/loki.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/grafana.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/alloy.nomad.hcl

# 4. Notifications + upload
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/ntfy.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/faasd.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/tusd.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/uppy.nomad.hcl
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/job-notifier.nomad.hcl

# 5. Auth (after tusd + traefik)
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl

# Optional: redis + postgres + docker-registry + Wave (experimental — see ../experimental/README.md)
# abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/redis.nomad.hcl
# abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/postgres.nomad.hcl
# abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/docker-registry.nomad.hcl
# abc admin services nomad cli -- job run -detach deployments/abc-nodes/experimental/nomad/wave.nomad.hcl
```

Check all service jobs at once:
```bash
abc admin services nomad cli -- job status -namespace services
```

---

## Experimental: Vault, Supabase, Wave

Job specs and scripts live under **`deployments/abc-nodes/experimental/`**. Default Caddy and Traefik configs do **not** expose these services; opt-in deploy and gateway notes are in **`../experimental/README.md`**.

### Vault — first-run initialization

After `job run` on **`../experimental/nomad/vault.nomad.hcl`**, Vault starts sealed and uninitialized:

```bash
export VAULT_ADDR=http://100.70.185.46:8200

vault operator init       # prints 5 unseal keys + root token — store in a safe place
vault operator unseal     # run 3× with 3 different key shares
vault operator unseal
vault operator unseal

vault secrets enable -path=secret kv-v2

# Wire into abc config
abc config set admin.services.vault.http http://100.70.185.46:8200
abc cluster capabilities sync
```

Health check uses `?standbyok=true&uninitcode=200&sealedcode=200` so the
Nomad deployment passes immediately even before init/unseal; Vault is fully
operational only after the three steps above.

---

## abc-nodes-auth — Traefik ForwardAuth

`abc-nodes-auth` is the authentication gateway between Traefik and tusd.
It validates `X-Nomad-Token` (or `Authorization: Bearer`) against the Nomad
`/v1/acl/token/self` endpoint and returns identity headers:

| Header | Value | Example |
|---|---|---|
| `X-Auth-User` | Nomad token Name | `su-mbhg-bioinformatics_alice` |
| `X-Auth-Group` | Group derived from policy | `su-mbhg-bioinformatics` |
| `X-Auth-Namespace` | Nomad namespace | `su-mbhg-bioinformatics` |

Verify it is working:
```bash
# No token → 401
curl -si http://100.70.185.46:9191/auth

# Valid member token → 200 + identity headers
curl -si -H "X-Nomad-Token: <token>" http://100.70.185.46:9191/auth
```

---

### Wave by Seqera — ⚠ WIP

`../experimental/nomad/wave.nomad.hcl` is written and tested locally but **not running** by default because
the Wave image is on a **private** registry (see job header). Older docs referenced `ghcr.io/seqeralabs/wave`; use the image URI from Seqera support.

**To enable:**

Option A — add auth block to `../experimental/nomad/wave.nomad.hcl`:
```hcl
config {
  image = "ghcr.io/seqeralabs/wave:v1.33.2"
  auth {
    username = "your-github-username"
    password = "ghp_YOUR_GITHUB_PAT"
  }
}
```

Option B — configure containerd on `aither` to authenticate:
```bash
ssh aither
sudo mkdir -p /etc/containerd/certs.d/ghcr.io
# Add credentials via containerd config or nerdctl login
```

Then deploy:
```bash
abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/wave.nomad.hcl
```

Wave runs in `lite` mode (no Tower/Platform), backed by the already-running
`abc-nodes-postgres` (`:5432`) and `abc-nodes-redis` (`:6379`).
Local `abc-nodes-docker-registry` on `:5000` is wired in as the image mirror.

---

## Observability helper scripts

| Script | Purpose |
|--------|---------|
| `nomad/scripts/deploy-observability-stack.sh` | MinIO → Prometheus → Loki → Grafana → Alloy |
| `nomad/scripts/validate-prometheus-abc-nodes.sh` | HTTP checks: scrape `up`, MinIO bucket metrics, Nomad cores PromQL |
| `nomad/scripts/redeploy-grafana-dashboards.sh` | `sync-grafana-definitions.sh` + `job run grafana.nomad.hcl` |
| `nomad/tests/workloads/submit-hello-world.sh` | Minimal `abc job run` smoke test (`--submit --watch`) |
| `nomad/tests/workloads/run-grafana-multi-user-burst.sh` | Parallel stress/hyperfine across `NS_USERS` namespaces |
| `../acl/apply-research-namespace-specs.sh` | `nomad namespace apply` for each `acl/namespaces/su-*.hcl` |

Full narrative: **`docs/abc-nodes-observability-and-operations.md`**.

---

## Operational notes

- **Platform jobs in `abc-services` namespace** — research-group tokens (member, submit,
  group-admin) cannot list or interact with these jobs. Only cluster / platform-admin
  policies can manage them.

- **Token for operations** — use the management token or the `cluster_services_admin`
  token. Both are in `~/.abc/config.yaml` (aither context) and `acl/tokens.env`.

- **Networking** — all jobs use `mode = "host"`. Ports are static. No CNI/bridge
  module required in the current single-node setup.

- **Vault health check** — uses `?uninitcode=200&sealedcode=200` (not the
  incorrect `uninitok`/`sealedok` params that pre-1.x docs sometimes show).

- **Secrets** — job defaults (MinIO root password etc.) are lab values only.
  Override with `-var` flags or migrate to Vault once initialized.

- **Capabilities sync** — after jobs are healthy:
  ```bash
  abc cluster capabilities sync
  abc cluster capabilities show
  ```
