# Nomad jobs — `abc-nodes`

Jobspecs in this directory + `experimental/` are deployed by the Terraform
config in `../terraform/`. Each one declares its target namespace explicitly:

| Namespace          | Jobspec location                                                     | Examples                                              |
| ------------------ | -------------------------------------------------------------------- | ----------------------------------------------------- |
| `abc-services`     | `nomad/*.nomad.hcl(.tftpl)` — most files in this directory           | traefik, rustfs, garage, prometheus, loki, grafana, alloy, ntfy, job-notifier, boundary-worker, docs, abc-backups |
| `abc-experimental` | `nomad/experimental/*.nomad.hcl.tftpl`                               | postgres, redis, wave, supabase, xtdb-v2, caddy-tailscale |
| `abc-automations`  | `nomad/fx/*.nomad.hcl`                                               | fx-notify, fx-tusd-hook, fx-archive                   |
| `abc-applications` | (reserved — no jobs yet)                                             |                                                       |

All four namespaces are write-accessible only to tokens carrying the
`services-admin` policy (i.e. cluster-admin tokens). Research-group tokens
cannot see or interact with these jobs.

Jobs use the community [**containerd-driver**](https://github.com/Roblox/nomad-driver-containerd)
(`driver = "containerd-driver"`) or `raw_exec` for lightweight shell processes
(plus `java` for jurist, deployed from a separate repo).
The Docker driver is **not** required and is not enabled on this cluster.

---

## Prerequisites

### 1. Nomad client plugins

- **containerd-driver** installed and enabled; **containerd** running.
- `containerd_runtime = "io.containerd.runc.v2"` in client plugin config.
- For `network { mode = "bridge" }`: **`bridge` kernel module** loaded + **CNI plugins** at `cni_path`.
  - Check: `journalctl -u nomad | grep bridge`
  - Fix: `sudo modprobe bridge && echo bridge | sudo tee /etc/modules-load.d/nomad-bridge.conf && sudo systemctl restart nomad`
  - **Bridge module IS required.** Most containerd-driver jobs (grafana,
    loki, prometheus, rustfs, xtdb, …) use `mode = "bridge"` with static
    port forwarding (`port "..." { static = N; to = N }`). `mode = "host"`
    does not actually put containerd-driver containers in the host netns —
    the container stays isolated and the bound port is unreachable on the
    host IP. Use bridge unless you're certain the driver supports host netns.
    Raw_exec / java tasks (alloy, traefik, jurist) can use either freely.

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
abc admin services nomad cli -- namespace list   # should show abc-services + abc-experimental + abc-automations
```

---

## Cluster layout (as deployed)

The unified Caddy gateway (`caddy_tailscale`) owns host **:80** across both
the LAN and Tailscale interfaces, fronting Traefik and routing the `*.aither`
vhost to the right backend. Direct host-port endpoints below are still
useful for diagnostics; production traffic should go through the Caddy
vhosts (see `terraform output public_endpoints`).

```
Nomad cluster:        http://100.70.185.46:4646   (single node: aither)
Caddy gateway HTTP:   http://100.70.185.46:80     ← unified entry; routes *.aither
Traefik HTTP:         http://100.70.185.46:8081   (dashboard: :8888)
MinIO S3 API:         http://100.70.185.46:9000   (basic-tier, abc CLI)
RustFS S3 API:        http://100.70.185.46:9900   (console: :9901)
Garage S3 API:        http://100.70.185.46:3900   (admin: :3903)
Grafana:              http://100.70.185.46:3000
Prometheus:           http://100.70.185.46:9090
Loki:                 http://100.70.185.46:3100
ntfy:                 http://100.70.185.46:8088
tusd:                 http://100.70.185.46:8080/files/  (basic-tier)
Docker Registry:      http://100.70.185.46:5000   (optional)
Supabase Kong:        http://100.70.185.46:8000   (when enable_supabase=true)
XTDB pgwire:          100.70.185.46:15432         (when enable_xtdb=true)
XTDB healthz:         http://100.70.185.46:5555/healthz/ready
```

Production-shape vhosts (resolved by the Tailscale MagicDNS or LAN /etc/hosts):
`grafana.aither`, `nomad.aither`, `consul.aither`, `traefik.aither`,
`rustfs.aither`, `garage.aither`, `docs.aither`, `ntfy.aither`,
`supabase.aither`, `xtdb.aither`, `jurist.aither`.

Opt-in experimental services (postgres, redis, wave, supabase, xtdb,
caddy_tailscale): see the **Experimental tier** rows in the inventory below
and `../terraform/README.md` for the `enable_*` toggles.

---

## Job inventory

Single source of truth: see `../terraform/main.tf` for the canonical list of
managed jobs (each `nomad_job "..."` resource is one entry below). Tier and
namespace come from that file; ports come from the jobspec.

### Enhanced tier — `abc-services` namespace (Terraform-managed)

| Job file | Job name | Host port(s) | Driver | Notes |
|---|---|---|---|---|
| `traefik.nomad.hcl` | `abc-nodes-traefik` | 8081, 8888 | raw_exec | Reverse proxy. Note: host **:80** is owned by `caddy_tailscale`, not traefik |
| `rustfs.nomad.hcl` | `abc-nodes-rustfs` | 9900, 9901 | containerd (bridge) | Hot-tier S3 |
| `garage.nomad.hcl` | `abc-nodes-garage` | 3900, 3902, 3903 | containerd (bridge) | Long-term archive S3 + admin API; bootstrap via poststart |
| `abc-nodes-docs.nomad.hcl` | `abc-nodes-docs` | (via Caddy vhost) | containerd | Docusaurus static site → http://docs.aither |
| `prometheus.nomad.hcl` | `abc-nodes-prometheus` | 9090 | containerd | |
| `loki.nomad.hcl` | `abc-nodes-loki` | 3100 | containerd | |
| `grafana.nomad.hcl(.tftpl)` | `abc-nodes-grafana` | 3000 | containerd | Dashboards baked in via templatefile |
| `alloy.nomad.hcl` | `abc-nodes-alloy` | 12345 | raw_exec, system job | |
| `ntfy.nomad.hcl` | `abc-nodes-ntfy` | 8088 | containerd | |
| `job-notifier.nomad.hcl(.tftpl)` | `abc-nodes-job-notifier` | — | raw_exec | Nomad event → ntfy bridge |
| `abc-nodes-auth.nomad.hcl` | `abc-nodes-auth` | 9191 | exec | Traefik ForwardAuth (basic-tier still uses) |
| `abc-backups.nomad.hcl` | `abc-backups` | — | containerd, batch | Periodic restic snapshots → garage |
| `boundary-worker.nomad.hcl` | `abc-nodes-boundary-worker` | — | exec, system job | |
| `docker-registry.nomad.hcl` | _(none — orphaned, see note below)_ | — | — | The legacy enhanced-tier jobspec is **superseded** by the experimental-tier templated version (`experimental/docker-registry.nomad.hcl.tftpl`) which uses bridge networking + a scratch volume_mount. The old file is kept for reference but not deployed by Terraform. |

Basic tier — `minio`, `tusd`, `uppy` — is owned by the **abc CLI** (deployed
with `abc admin services nomad cli -- job run …`). They live in `abc-services`
too but are **not** in this Terraform config.

### Experimental tier — `abc-experimental` namespace (opt-in)

| Job file | Job name | Host port(s) | Driver | Notes |
|---|---|---|---|---|
| `experimental/postgres.nomad.hcl.tftpl` | `abc-experimental-postgres` | 5432 | containerd (bridge) | Standalone vanilla postgres (NOT the supabase-bundled one) |
| `experimental/redis.nomad.hcl.tftpl` | `abc-experimental-redis` | 6379 | containerd | |
| `experimental/wave.nomad.hcl.tftpl` | `abc-experimental-wave` | (Seqera-defined) | containerd | Needs postgres + redis |
| `experimental/supabase.nomad.hcl.tftpl` | `abc-experimental-supabase` | 8000 (Kong) | containerd (bridge) | 6-task group: db + db-init + meta + auth + rest + studio + kong |
| `experimental/xtdb-v2.nomad.hcl.tftpl` | `abc-experimental-xtdb` | 5555, 15432 | containerd (bridge) | Pinned to aither; pgwire backend for jurist |
| `experimental/docker-registry.nomad.hcl.tftpl` | `abc-experimental-docker-registry` | 5000 | containerd (bridge) | Local OCI registry. Pinned to aither, persists on scratch. See `../terraform/README.md` "Local registry workflow" |
| `experimental/caddy-tailscale.nomad.hcl` | `abc-experimental-caddy-tailscale` | 80 | raw_exec | **Production gateway** — owns host :80 across LAN + Tailscale, fronts traefik + *.aither vhosts |

### Automations tier — `abc-automations` namespace

| Job file | Job name | Host port(s) | Driver | Notes |
|---|---|---|---|---|
| `fx/fx-notify.nomad.hcl` | `fx-notify` | dynamic | raw_exec | Webhook → ntfy |
| `fx/fx-tusd-hook.nomad.hcl` | `fx-tusd-hook` | dynamic | raw_exec | tusd post-finish webhook |
| `fx/fx-archive.nomad.hcl` | `fx-archive` | — | raw_exec, batch | Periodic RustFS → Garage tier-down |

### Old experimental folder

`../experimental/` is the **pre-Terraform** opt-in directory (Vault / Supabase
/ Wave / faasd written manually). Most of its content has been superseded by
the Terraform-managed experimental tier above. Treat `../experimental/README.md`
as historical — the Terraform config in `../terraform/` is the source of truth
for postgres/redis/wave/supabase/xtdb/caddy_tailscale today.

### Experimental tier (`abc-experimental` namespace, Terraform-managed)

A separate set of opt-in services lives under `nomad/experimental/` and is
provisioned via Terraform (`enable_<name>` toggles, all default `false`).
See **`../terraform/README.md`** for the full list.

| Job file | Job name | Port(s) | Status | Notes |
|---|---|---|---|---|
| `experimental/xtdb-v2.nomad.hcl.tftpl` | `abc-experimental-xtdb` | 5555, 15432 | ✅ on aither | XTDB v2 bitemporal DB; pgwire backend for jurist |

The `abc-jurist-svc` package's `abc-experimental-jurist` job (java driver,
deployed manually via `nomad job run` from that repo) connects to
`abc-experimental-xtdb` over pgwire on `localhost:15432` — both pinned to
aither.

---

## Deployment order

The enhanced + experimental + automations tiers are deployed via Terraform
in `../terraform/`, which encodes the dependency order via `depends_on` —
so `terraform apply` IS the deployment-order tool. See
`../terraform/README.md` for the canonical sequence and the dependency
graph diagram.

```bash
# From the abc-cluster-cli repo root, with aither context active:
cd deployments/abc-nodes/terraform
abc admin services cli terraform -- apply -auto-approve

# Opt into experimental services on top:
abc admin services cli terraform -- apply -auto-approve \
  -var=enable_xtdb=true \
  -var=enable_postgres=true \
  -var=enable_supabase=true
```

Basic-tier (`minio`, `tusd`, `uppy`) is **not** Terraform-managed and is
expected to be running already. Bring it up manually only if it's missing:

```bash
# Basic tier (manual, not Terraform):
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/tusd.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/uppy.nomad.hcl
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl
```

Check all service jobs at once:
```bash
abc admin services nomad cli -- job status -namespace services
```

---

## Experimental: Vault (legacy manual), and the Terraform-managed tier

There are two parallel "experimental" trees in this repo — they are
**different scopes** despite the similar name:

1. **`../experimental/`** (the *legacy* directory) — manually-deployed
   Vault + faasd + early Supabase/Wave attempts. Operator deploys with
   `nomad job run`. The Vault initialization runbook below still applies.
   See `../experimental/README.md`.

2. **`nomad/experimental/`** (this directory's `experimental/` subfolder)
   — Terraform-managed opt-in services with `enable_<name>` flags:
   postgres, redis, wave, supabase, xtdb, caddy_tailscale, restic-server.
   Operator deploys with `terraform apply -var=enable_xtdb=true …`.
   See `../terraform/README.md`.

The Terraform tier is the source of truth for postgres / redis / wave /
supabase / xtdb today; the legacy tree is kept for Vault and any service
that has not yet been Terraform-ported.

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

The current Terraform-managed Wave jobspec lives at
`experimental/wave.nomad.hcl.tftpl` (Terraform `enable_wave = true`); the
older manual one at `../experimental/nomad/wave.nomad.hcl` is superseded.
Wave is **not running** by default because the image is on a **private**
registry (see job header).

**To enable:**

```bash
# Set the right wave image + tag, then:
abc admin services cli terraform -- apply -auto-approve \
  -var=enable_postgres=true -var=enable_redis=true -var=enable_wave=true \
  -var=wave_image=<tower-supplied-image-URI>
```

If your image is on a private registry, configure containerd auth on aither:
```bash
ssh aither
sudo mkdir -p /etc/containerd/certs.d/ghcr.io
# Add credentials via containerd config or nerdctl login
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

- **Networking** — most containerd-driver jobs use `mode = "bridge"` with
  static port forwarding (`{ static = N; to = N }`). Raw_exec and java
  tasks use `mode = "host"`. The `bridge` kernel module is required —
  see Prerequisites above.

- **Known broken-pattern jobs** — `experimental/redis.nomad.hcl.tftpl`
  combines containerd-driver with `mode = "host"`, which leaves the
  container in its own netns and makes the bound port unreachable on the
  host IP. It appears "running" in `nomad status` but external connections
  to its port fail. Convert to bridge (`{ static = N; to = N }`) before
  relying on it — the same pattern grafana / loki / prometheus / xtdb /
  supabase / docker-registry (experimental tier) all use.

  The legacy enhanced-tier `docker-registry.nomad.hcl` had this same bug
  and is no longer deployed; `enable_docker_registry` now points at the
  fixed `experimental/docker-registry.nomad.hcl.tftpl`.

- **Vault health check** — uses `?uninitcode=200&sealedcode=200` (not the
  incorrect `uninitok`/`sealedok` params that pre-1.x docs sometimes show).

- **Secrets** — job defaults (MinIO root password etc.) are lab values only.
  Override with `-var` flags or migrate to Vault once initialized.

- **Capabilities sync** — after jobs are healthy:
  ```bash
  abc cluster capabilities sync
  abc cluster capabilities show
  ```
