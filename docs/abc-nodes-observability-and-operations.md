# abc-nodes Observability and Operations

This document ties together **Prometheus**, **Grafana**, **Nomad metrics**, **MinIO metrics**, **research namespaces**, and **workload validation** for the `abc-nodes` floor. It complements `docs/abc-nodes-testing-setup.md` (workloads and MinIO IAM) and `deployments/abc-nodes/acl/README.md` (ACL design).

## Architecture (short)

| Component | Nomad job (typical) | Purpose |
|-----------|-------------------|---------|
| Prometheus | `abc-nodes-prometheus` | Scrapes self, Nomad `/v1/metrics`, MinIO cluster + bucket metrics |
| Grafana | `abc-nodes-grafana` | Provisioned dashboards; Prometheus + Loki datasources |
| Loki / Alloy | `abc-nodes-loki`, `abc-nodes-alloy` | Logs (not detailed here) |

Deploy order for the metrics stack: see `deployments/abc-nodes/nomad/scripts/deploy-observability-stack.sh` (MinIO → Prometheus → Loki → Grafana → Alloy).

## Prometheus integration

### Scrape jobs (`prometheus.nomad.hcl`)

The bundled Prometheus template defines:

| `job` | Target | Path / notes |
|-------|--------|----------------|
| `prometheus` | `127.0.0.1:9090` | Self-scrape |
| `nomad` | Nomad HTTP RPC host `:4646` | `GET /v1/metrics?format=prometheus` |
| `minio` | MinIO API `:9000` | `GET /minio/v2/metrics/cluster` |
| `minio_bucket` | MinIO API `:9000` | `GET /minio/v2/metrics/bucket` (per-bucket series) |

**Operator action:** the Nomad job uses **static** `targets` hostnames/IPs in the embedded `prometheus.yml` template. When the Nomad/MinIO host changes, update `deployments/abc-nodes/nomad/prometheus.nomad.hcl` (or refactor to a Nomad `variable` + `-var` at deploy time) and redeploy `abc-nodes-prometheus`.

### MinIO metrics authentication

`deployments/abc-nodes/nomad/minio.nomad.hcl` sets `MINIO_PROMETHEUS_AUTH_TYPE=public` so Prometheus can scrape `/minio/v2/metrics/*` without bearer tokens on the **lab** network. Tighten this (JWT / network ACL / reverse-proxy auth) before production exposure.

### Validation script

After deploy or IP changes:

```bash
# Optional: URL that serves Prometheus /api/v1 (e.g. via Traefik)
export PROMETHEUS_QUERY_BASE="http://aither.mb.sun.ac.za/prometheus"

bash deployments/abc-nodes/nomad/scripts/validate-prometheus-abc-nodes.sh
```

The script checks `up` for the expected scrape jobs and runs a few instant queries (including a Nomad MHz→cores sanity query).

## Grafana dashboards

### Provisioned JSON

| File | UID (in JSON) | Role |
|------|----------------|------|
| `deployments/abc-nodes/nomad/grafana-dashboard-usage-overview.json` | `abc-nodes-usage-overview` | Admin usage: capacity, namespaces, research users, queues |
| `deployments/abc-nodes/nomad/grafana-dashboard-bucket-usage.json` | `abc-nodes-bucket-usage` | MinIO bucket size / objects / distributions (needs `minio_bucket` scrape) |
| `deployments/abc-nodes/nomad/grafana-dashboard-abc-nodes.json` | *(see JSON)* | Broader cluster view (legacy MHz panels on some rows) |

Provisioning is wired in `deployments/abc-nodes/nomad/grafana.nomad.hcl` (templates under `/etc/grafana/provisioning/...`).

### Keeping variables in sync

Dashboard template variables (`namespace`, `research_user`, `bucket`) are maintained as **custom** lists derived from ACL / MinIO bootstrap sources:

```bash
bash deployments/abc-nodes/nomad/sync-grafana-definitions.sh
```

Sources:

- Namespaces: `deployments/abc-nodes/acl/namespaces/*.hcl` (excluding `abc-services` / `abc-applications`)
- Users: `NS_USERS[...]` map in `deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh`
- Buckets: same names as research namespaces

### Redeploy Grafana after dashboard edits

```bash
bash deployments/abc-nodes/nomad/scripts/redeploy-grafana-dashboards.sh
```

Or manually: run `sync-grafana-definitions.sh` when principals/namespaces change, then `abc admin services nomad cli -- job run deployments/abc-nodes/nomad/grafana.nomad.hcl`.

### Usage overview — CPU “cores” panels (PromQL)

Nomad exposes client CPU in **MHz** (`nomad_client_allocated_cpu`, `nomad_client_allocs_cpu_allocated`, etc.). To show **approximate cores**, panels multiply MHz by `(logical_cpu_count / total_schedulable_mhz)` on the same `host`:

```promql
nomad_client_allocated_cpu
* on(host) group_left()
(
  count by (host) (nomad_client_host_cpu_total)
  / on(host) (nomad_client_allocated_cpu + nomad_client_unallocated_cpu)
)
```

**Important:** `group_left` must be `group_left()` or `group_left(label, ...)`, not `group_left (expr ...)`. The latter is invalid PromQL and Grafana shows empty panels.

## Research Nomad namespaces

### Capability: `containerd-driver`

Stress/hyperfine test scripts use `#ABC --driver=containerd`. Each research namespace must list `containerd-driver` in `capabilities.enabled_task_drivers` or job registration fails with *used task driver "containerd-driver" is not allowed*.

Namespace specs live in `deployments/abc-nodes/acl/namespaces/`. Apply all research (`su-*.hcl`) definitions:

```bash
export ABC_ACTIVE_CONTEXT=abc-cluster-admin   # or any context with namespace apply rights (see note below)
bash deployments/abc-nodes/acl/apply-research-namespace-specs.sh
```

**Context env:** the `abc` binary reads **`ABC_ACTIVE_CONTEXT`** to override `active_context` in `~/.abc/config.yaml`. The burst script also treats **`ABC_CONTEXT`** as an alias and exports `ABC_ACTIVE_CONTEXT` if the latter is unset. The apply script only touches `acl/namespaces/su-*.hcl` (research groups).

## Multi-user load for Grafana QA

To submit overlapping `stress-ng` / `hyperfine` jobs across **real** `(namespace, su-<ns>_<user>)` pairs parsed from `setup-minio-namespace-buckets.sh`:

```bash
export ABC_ACTIVE_CONTEXT=abc-cluster-admin
bash deployments/abc-nodes/nomad/tests/workloads/run-grafana-multi-user-burst.sh
```

Options (environment):

| Variable | Default | Meaning |
|----------|---------|---------|
| `ABC_ACTIVE_CONTEXT` | *(unset)* | Selects config context for `abc` (preferred). |
| `ABC_CONTEXT` | *(unset)* | Burst script only: if set and `ABC_ACTIVE_CONTEXT` is empty, copied to `ABC_ACTIVE_CONTEXT`. |
| `ABC_BURST_INCLUDE_HYPERFINE` | `1` | Also submit `hyperfine-micro-default.sh` per user |
| `ABC_BURST_STRESS_TIME` | `00:15:00` | Nomad walltime passed to `abc job run --time` |
| `ABC_BURST_NAME_TAG` | `grafana-burst` | Fragment in `--name` for job naming |

Job names follow `script-job-<principal>--<tag>-<abc suffix>` so usage dashboards can `label_replace` `research_user` from `exported_job`.

## MinIO layout (research buckets)

Per namespace bucket (name = namespace), object layout is:

- **Private:** `users/<username>/…`
- **Collaboration:** `shared/<username>/…` (writable only in own subtree; other `shared/*` readable per IAM policies from `setup-minio-namespace-buckets.sh`)

Only **`users/`** and **`shared/`** should exist at bucket root for member workflows (group admins retain full prefix access).

## Related files (index)

| Path | Role |
|------|------|
| `deployments/abc-nodes/nomad/prometheus.nomad.hcl` | Prometheus job + scrape config |
| `deployments/abc-nodes/nomad/minio.nomad.hcl` | MinIO + `MINIO_PROMETHEUS_AUTH_TYPE` |
| `deployments/abc-nodes/nomad/grafana.nomad.hcl` | Grafana + dashboard provisioning |
| `deployments/abc-nodes/nomad/sync-grafana-definitions.sh` | Sync dashboard variables from ACL |
| `deployments/abc-nodes/nomad/scripts/validate-prometheus-abc-nodes.sh` | Smoke Prometheus |
| `deployments/abc-nodes/nomad/scripts/redeploy-grafana-dashboards.sh` | Sync + redeploy Grafana |
| `deployments/abc-nodes/acl/apply-research-namespace-specs.sh` | Apply `su-*.hcl` namespaces |
| `deployments/abc-nodes/nomad/tests/workloads/run-grafana-multi-user-burst.sh` | Parallel validation jobs |
| `docs/abc-nodes-testing-setup.md` | Workloads, MinIO bootstrap, expectations |
