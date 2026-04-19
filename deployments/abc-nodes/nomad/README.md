# Nomad jobs for `abc-nodes` floor services

These job definitions run **object storage**, **tus**, and **observability / notifications** as Nomad **service** jobs using the community [**containerd** task driver](https://github.com/Roblox/nomad-driver-containerd) (`driver = "containerd-driver"`), not the built-in Docker driver. They are intended for **`cluster_type: abc-nodes`** contexts: a small Nomad footprint where you still want durable-ish services without the full `abc-cluster` control plane.

## Prerequisites

1. **Nomad clients** with the **containerd-driver** plugin installed and enabled, **containerd** running, and outbound registry access (or pre-pulled images). Client plugin config must set **`containerd_runtime`** (commonly `io.containerd.runc.v2`). For **`network { mode = "bridge" }`** you need **both**:
   - **Linux `bridge` kernel module** loaded (or loadable) so Nomad’s **bridge** fingerprinter adds `mode=bridge` to the node; otherwise the scheduler reports **Constraint `"missing network"`** even with CNI installed. Check `journalctl -u nomad` for `failed to detect bridge kernel module`. Fix: `sudo modprobe bridge`, persist e.g. `echo bridge | sudo tee /etc/modules-load.d/nomad-bridge.conf`, then **`sudo systemctl restart nomad`**. `abc infra compute add` with CNI install configures this automatically on supported paths.
   - **CNI reference plugins** on the client at **`cni_path`** (e.g. `/opt/cni/bin`). **`host_network = true`** in the task `config` block is an alternative if you accept host networking (then use **`network { mode = "host" }`** or drop the group `network` stanza per your needs).
2. **Host volumes** (or replace with CSI / bind mounts you already operate). Example client fragment:

   ```hcl
   host_volume "abc-nodes-minio" {
     path      = "/var/lib/abc-nodes/minio"
     read_only = false
   }
   host_volume "abc-nodes-rustfs" {
     path      = "/var/lib/abc-nodes/rustfs"
     read_only = false
   }
   host_volume "abc-nodes-prometheus" {
     path      = "/var/lib/abc-nodes/prometheus"
     read_only = false
   }
   host_volume "abc-nodes-grafana" {
     path      = "/var/lib/abc-nodes/grafana"
     read_only = false
   }
   host_volume "abc-nodes-loki" {
     path      = "/var/lib/abc-nodes/loki"
     read_only = false
   }
   host_volume "abc-nodes-ntfy" {
     path      = "/var/lib/abc-nodes/ntfy"
     read_only = false
   }
   ```

   Match **volume `source`** names in each job file (`abc-nodes-minio`, etc.). Tusd uses ephemeral disk only in the shipped spec (no host volume); add one if you need cached uploads on disk.

3. **abc config** with `cluster_type: abc-nodes` (optional for placement; required for your operational convention) and a Nomad-capable context (`nomad_addr`, `nomad_token`, …).

## Static credentials in `~/.abc/config.yaml` (abc-nodes)

For **`cluster_type: abc-nodes`** contexts you can persist operator credentials under **`contexts.<name>.admin.abc_nodes`** so they survive shell restarts and match what you pass into Nomad jobs (`-var minio_root_user=…`, tusd env, etc.):

| YAML field | Effect |
|------------|--------|
| `nomad_namespace` | Exported as **`NOMAD_NAMESPACE`** for `abc admin services nomad cli` when that env var is not already set (e.g. `default` or a dedicated namespace). |
| `s3_access_key` / `s3_secret_key` | Merged into **`AWS_ACCESS_KEY_ID`** / **`AWS_SECRET_ACCESS_KEY`** for `abc admin services minio cli` and **`rustfs cli`** when those env vars are unset. |
| `s3_region` | Sets **`AWS_DEFAULT_REGION`** when unset. |
| `admin.services.minio.endpoint` | MinIO **S3 API** base URL (no trailing slash). Merged into **`AWS_ENDPOINT_URL`** / **`AWS_ENDPOINT_URL_S3`** for **`minio cli`** only. |
| `admin.services.rustfs.endpoint` | RustFS **S3 API** base URL for **`rustfs cli`** only (so MinIO and RustFS can coexist on one context). |
| `admin.services.rustfs.http` | RustFS **web console** URL (browser login; enable with `RUSTFS_CONSOLE_ENABLE`; job maps host `console` → container **9001**). |
| `minio_root_user` / `minio_root_password` | Used as **`MINIO_ROOT_*`** when set; if `s3_*` keys are omitted, they also supply the AWS-style keys above. |

Process environment always wins over config for the same variable name. Set credentials with e.g. `abc config set contexts.<name>.admin.abc_nodes.s3_access_key '…'` and S3 bases with `abc config set contexts.<name>.admin.services.minio.endpoint 'http://…'` (see `abc config set --help`). Populate service URLs from Nomad with **`abc admin services config sync`**. **Lab warning:** these values are plaintext on disk; restrict file permissions (`0600`) and do not commit real secrets.

## Nomad Pack bundles (`abc-nodes-base` / `abc-nodes-enhanced`)

Pack names must use underscores for [`nomad-pack`](https://github.com/hashicorp/nomad-pack). Two curated packs live under **`nomad-packs/`**:

| Directory / pack name   | Maps to cluster idea   | Jobs rendered |
|-------------------------|------------------------|----------------|
| `nomad-packs/abc_nodes_base` | **abc-nodes-base** (minimal floor) | `minio`, `tusd` |
| `nomad-packs/abc_nodes_enhanced` | **abc-nodes-enhanced** (floor + monitoring) | same as base plus **Prometheus, Loki, Grafana, Alloy** (matches `AbcNodesClusterFloorEnhanced`: metrics, logs, Alloy) |

Render defaults to HCL job files, then submit with the Nomad CLI (or `nomad-pack run` for pack-managed lifecycle):

```bash
cd deployments/abc-nodes/nomad-packs/abc_nodes_base
nomad-pack render . -o /tmp/abc-nodes-base-render -y
abc admin services nomad cli -- job validate /tmp/abc-nodes-base-render/abc_nodes_base/minio.nomad
abc admin services nomad cli -- job run -detach /tmp/abc-nodes-base-render/abc_nodes_base/minio.nomad
# … then tusd.nomad with -var or a var-file for minio_s3_endpoint, credentials, etc.

cd ../abc_nodes_enhanced
nomad-pack render . -o /tmp/abc-nodes-enh-render -y \
  --var='nomad_token=<your-nomad-acl-token>'
# Order: minio → (create `loki` bucket) → prometheus → loki → grafana → tusd → alloy (see table below).
```

Override any pack default with `nomad-pack render … --var key=value` or `-f overrides.hcl` (HCL attribute syntax). **Enhanced** defaults assume Prometheus/Loki/Grafana listen on `127.0.0.1` from the host (Alloy uses `raw_exec` + host network); adjust `grafana_*_url`, `loki_minio_endpoint`, `nomad_addr`, and remote-write URLs for your topology.

## Scripted deploy + E2E

From the **`abc-cluster-cli` repo root**:

```bash
./deployments/abc-nodes/nomad/scripts/deploy-observability-stack.sh
./deployments/abc-nodes/nomad/scripts/e2e-observability-stack.sh   # needs Go + ~/.abc context
```

Set `ABC_NODES_ALLOY_PURGE_FOR_SYSTEM=0` to skip the automatic Alloy **service→system** migration (purge).

## Submit with `abc admin services nomad cli`

The Nomad CLI is invoked as a passthrough; use **`abc admin services nomad cli -- …`** so everything after `--` is forwarded verbatim to `nomad` (same argv as upstream). Address and token default from the active abc context (override with `NOMAD_ADDR` / `NOMAD_TOKEN` or admin flags on the parent command).

From the **repository root** (`analysis/packages/abc-cluster-cli`):

```bash
# Validate
abc admin services nomad cli -- job validate deployments/abc-nodes/nomad/minio.nomad.hcl

# Run (detached)
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/minio.nomad.hcl

# Plan then apply (when supported by your nomad binary)
abc admin services nomad cli -- job plan  deployments/abc-nodes/nomad/grafana.nomad.hcl
abc admin services nomad cli -- job run   deployments/abc-nodes/nomad/grafana.nomad.hcl
```

Override defaults (datacenter, image tags, secrets) with `-var` / `-var-file` per Nomad’s job spec variables, for example:

```bash
abc admin services nomad cli -- job run -detach \
  -var='datacenters=["default"]' \
  -var="minio_root_password=change-me" \
  deployments/abc-nodes/nomad/minio.nomad.hcl
```

## Order and wiring

| Order | Job file              | Notes |
|------|------------------------|--------|
| 1    | `minio.nomad.hcl`      | S3 API + console; create buckets (e.g. `tusd`) with `mc` or a follow-up batch job. |
| 2    | `rustfs.nomad.hcl`   | Optional S3-compatible store; uses distinct ports from MinIO. |
| 3    | `tusd.nomad.hcl`     | Set `-var minio_s3_endpoint=...` to the MinIO **S3 API** host port (job port `api`, in-container `:9000`), not the console port. Use **no trailing slash**. From bridge networking, the node’s **Tailscale IP can hairpin-fail** to itself — prefer a **LAN IP** or route that works from the allocation. Ensure bucket `tusd` (or `s3_bucket`) exists. |
| 4    | `prometheus.nomad.hcl` | Scrapes itself; extend `prometheus.yml` template for node_exporter / Nomad metrics. |
| 5    | `loki.nomad.hcl`     | Single-store dev-style config; tune for production. |
| 6    | `grafana.nomad.hcl`  | Set `grafana_admin_password`; datasources + **Nomad allocation logs** dashboard are provisioned (Loki URL must include `/loki` when Loki uses `path_prefix`). |
| 7    | `ntfy.nomad.hcl`     | `ntfy serve` behind HTTP port. |
| 8    | `vault.nomad.hcl`    | HashiCorp Vault with **Raft integrated storage** on **:8200**. Data persists to `/opt/nomad/vault/data` on the host (override: `-var vault_data_dir=/your/path`). **First run:** `vault operator init` → save 5 unseal keys + root token; `vault operator unseal` ×3; `vault secrets enable -path=secret kv-v2`. Wire into abc: `abc config set admin.services.vault.http http://<ip>:8200 && abc cluster capabilities sync`. See the file header for the full workflow. |
| 9    | `traefik.nomad.hcl`  | Host-network reverse proxy; register after backends so `nomadService` templates resolve. Dashboard **8888**, HTTP entry **80**; `abc cluster capabilities sync` writes `admin.services.traefik.http`. |
| 10   | `job-notifier.nomad.hcl` | Streams the Nomad `/v1/event/stream?topic=Allocation` feed (raw_exec, host network) and POSTs to **ntfy** when any alloc reaches `complete`, `failed`, or `lost`. Requires `jq` on the host. Topic defaults to `abc-jobs` on port 8088. Submit after ntfy. |

**`alloy.nomad.hcl`:** submit **after** Prometheus + Loki + Grafana. **System** job (one alloc per node); tails **`/opt/nomad/data/...`** and **`/var/lib/nomad/...`** alloc log globs. Labels `alloc_id`, `task`, and `stream` are extracted from the `filename` label in `loki.process` — the pipeline source must be `"filename"` (not `"__path__"`, which is an internal discovery label not forwarded to pipeline stages). Migrating from an older **service**-typed Alloy: Nomad cannot change type in place — use `scripts/deploy-observability-stack.sh` (purges when `ABC_NODES_ALLOY_PURGE_FOR_SYSTEM=1`) or `nomad job stop -purge abc-nodes-alloy` then re-run.

**`grafana-dashboard-abc-nodes.json`:** pre-built Grafana dashboard for the abc-nodes enhanced stack. Import via `POST /api/dashboards/import` or the Grafana UI (`+` → Import → Upload JSON). Covers cluster health stats, node CPU/memory/disk/network, job and alloc status, scheduler RPC latency, and a Loki log panel filtered by `task` and `stream`. Variables: `node`, `job`, `task`.

## Capabilities sync

After the floor jobs are running, let the CLI discover what is available:

```bash
abc cluster capabilities sync
```

This queries the Nomad service registry (falls back to job listing on 403) and writes two things to the active context config:

1. **`capabilities.*`** — a structured record of which services are running (`storage`, `uploads`, `logging`, `monitoring`, `observability`, `notifications`, `secrets`, `proxy`).
2. **`admin.services.<svc>.http|endpoint`** — the discovered URL for each service (never overwrites values you set manually).

Once synced:
- `abc data upload` automatically uses `admin.services.tusd.http/files/` as the tus endpoint (no `--endpoint` flag needed).
- `abc secrets set/get/list/delete --backend vault` resolves the Vault address from `admin.services.vault.http`.
- `abc pipeline run` with `secret://name` params translates to the backend indicated by `capabilities.secrets`.
- `capabilities.notifications` is set when ntfy is running; `job-notifier` sends push notifications to the `abc-jobs` ntfy topic on job completion/failure.

View the stored state at any time:

```bash
abc cluster capabilities show
```

## “All other tools”

These specs cover the **floor stack** called out for `abc-nodes` (storage, tus, metrics, logs, push, Vault, Traefik). **CLI-style tools** under `abc admin services` (Nomad / MinIO **mc** / RustFS / Vault / Nebula / Tailscale / Rclone / **Traefik** passthroughs) are thin wrappers around upstream binaries; floor **URLs and tokens** for Vault merge from `~/.abc` when `cluster_type` is `abc-nodes`.

For **Tailscale**, **Nebula**, and similar, use upstream Nomad examples or vendor packs if you need them as jobs. Production Vault should follow HashiCorp’s reference architecture instead of the bundled single-node Raft job.

## Operational notes

- **Placement: `Constraint "missing network"`:** almost always means the Nomad client never fingerprinted **bridge** mode — commonly the **`bridge` kernel module** was not loaded on Linux. CNI binaries alone are not sufficient for the scheduler check. See prerequisite (1) above.
- **Secrets:** defaults in these files are for **lab** use only. Replace with Vault / Nomad Variables / `-var` for real clusters.
- **Networking:** jobs publish **dynamic** ports by default. Put a load balancer or static `reserved` ports in `network` if you need stable addresses for tusd → MinIO.
- **RustFS UID:** bind-mounted data dirs may need ownership compatible with the container user (see RustFS Docker docs).
