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
| 6    | `grafana.nomad.hcl`  | Set `grafana_admin_password`; add Prometheus datasource in UI or provisioning later. |
| 7    | `ntfy.nomad.hcl`     | `ntfy serve` behind HTTP port. |
| 8    | `traefik.nomad.hcl`  | Host-network reverse proxy; register after backends so `nomadService` templates resolve. Dashboard **8888**, HTTP entry **80**; `abc admin services config sync` writes `admin.services.traefik.http` / `endpoint`. |

## “All other tools”

These specs cover the **floor stack** called out for `abc-nodes` (storage, tus, metrics, logs, push, Traefik). **CLI-style tools** under `abc admin services` (Nomad / MinIO **mc** / RustFS / Vault / Nebula / Tailscale / Rclone / **Traefik** passthroughs) are **not** modeled here: they are thin wrappers around upstream binaries for operator use, not long-running cluster services.

For **Vault**, **Tailscale**, **Nebula**, and similar, use upstream Nomad examples or vendor packs if you need them as jobs; production Vault especially should follow HashiCorp’s reference architecture, not a minimal single-task template.

## Operational notes

- **Placement: `Constraint "missing network"`:** almost always means the Nomad client never fingerprinted **bridge** mode — commonly the **`bridge` kernel module** was not loaded on Linux. CNI binaries alone are not sufficient for the scheduler check. See prerequisite (1) above.
- **Secrets:** defaults in these files are for **lab** use only. Replace with Vault / Nomad Variables / `-var` for real clusters.
- **Networking:** jobs publish **dynamic** ports by default. Put a load balancer or static `reserved` ports in `network` if you need stable addresses for tusd → MinIO.
- **RustFS UID:** bind-mounted data dirs may need ownership compatible with the container user (see RustFS Docker docs).
