# abc-nodes Test Setup and Workload Validation

This document describes the current end-to-end test setup for `abc-nodes` workload simulation, including stress-ng/hyperfine scripts, namespace strategy, driver requirements, and observability dashboards.

**Operators:** for Prometheus scrape layout, Grafana redeploy, namespace capability checks, and multi-user Grafana validation scripts, see **`docs/abc-nodes-observability-and-operations.md`**.

## Goals

- Generate realistic multi-user cluster load for admin observability.
- Validate `abc job run` behavior with templated workload scripts on Nomad.
- Exercise usage dashboards (group/institute/research-user views) with real data.

## Current Workload Suite

Workload scripts live in:

- `deployments/abc-nodes/nomad/tests/workloads/`

Included scripts:

- Stress workloads:
  - `stress-ng-cpu-default.sh`
  - `stress-ng-cpu-services.sh`
  - `stress-ng-cpu-hpc.sh`
  - `stress-ng-cpu-abc-context.sh`
  - `stress-ng-cpu-user-uh-bristol-animaltb-hpc_alice.sh`
  - `stress-ng-cpu-user-stanford-genetics-bioinfo_bob.sh`
  - `stress-ng-cpu-user-su-sdsct-ceri_tj.sh`
- Hyperfine workloads:
  - `hyperfine-micro-default.sh`
  - `hyperfine-micro-services.sh`
  - `hyperfine-micro-user-oxford-neurodegen-neuropsychiatry_charlie.sh`
  - `hyperfine-micro-user-su-sdsct-ceri_eduan.sh`

## Runtime Image and Driver Model

- OCI image used for both stress-ng and hyperfine:
  - `community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8`
- Default driver for these test scripts:
  - `containerd-driver`

Important path behavior:

- Workload task scripts are rendered under Nomad `local/`.
- For OCI drivers, script execution uses `$${NOMAD_TASK_DIR}/<script>.sh`.
- This avoids `WORKDIR` and `/local/local/...` mismatches seen with relative `local/...` paths.

## Randomized Multi-User Test Mode (Default)

`submit-all.sh` now defaults to randomized profile overrides to simulate real-world mixed usage:

- Script: `deployments/abc-nodes/nomad/tests/workloads/submit-all.sh`
- Default flags: `--submit`
- Randomized overrides:
  - CPU, memory, walltime profile per script type
  - Synthetic `research_user` for non-user scripts (legacy demo names; not the same as live `su-*_*` IAM principals)
  - Metadata tags:
    - `test_mode=multi_user_random`
    - `test_seed=<seed>`

For **real** research namespaces and `NS_USERS` principals (matching MinIO / Grafana variable lists), use:

- `deployments/abc-nodes/nomad/tests/workloads/run-grafana-multi-user-burst.sh` (see observability doc).

Controls:

- Deterministic run:
  - `ABC_SEED=123 ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh`
- Disable randomization:
  - `ABC_RANDOMIZE=0 ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh`
- Custom submit/watch flags:
  - `ABC_JOB_FLAGS="--submit --watch" ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh`

## Namespace Strategy for Research Groups

### Naming Pattern

Use `su-mbhg-<group>` namespace names for research-group isolation.

Examples:

- `su-mbhg-bioinformatics`
- `su-mbhg-hostgen`
- `su-mbhg-animaltb`
- `su-psy-neuropsychiatry`
- `su-sdsct-ceri`

### Rename Guidance

Nomad namespaces are not renamed in place. To migrate naming:

1. Create/apply target namespace(s) in the new naming pattern.
2. Submit new jobs into target namespaces.
3. Retire old namespaces when no longer needed.

### Driver Capabilities

For stress/hyperfine test jobs in `su-mbhg-*` namespaces, ensure namespace capabilities allow `containerd-driver`.

If a namespace only permits `exec`/`docker` and cluster nodes lack healthy Docker driver, submissions fail.

## Typical Validation Commands

### Submit one user workload to a specific research-group namespace

`go run . job run deployments/abc-nodes/nomad/tests/workloads/stress-ng-cpu-user-uh-bristol-animaltb-hpc_alice.sh --namespace su-mbhg-animaltb --submit`

### Submit matrix across all `su-mbhg-*` namespaces

Use a shell loop over `abc admin services nomad cli -- namespace list`, filtered by `^su-mbhg-`, and submit the user scripts with `--namespace "$ns"`.

### Check terminal status

`go run . job show <job-id> --namespace <namespace>`

Successful batch completion appears as:

- `Status = dead`
- Task-group summary with `SUCCEEDED=1 FAILED=0`

### Check logs

`go run . job logs <job-id> --namespace <namespace> --type stdout`

## Dashboard Coverage

Grafana Nomad job:

- `deployments/abc-nodes/nomad/grafana.nomad.hcl`

Usage dashboard JSON:

- `deployments/abc-nodes/nomad/grafana-dashboard-usage-overview.json`
- `deployments/abc-nodes/nomad/grafana-dashboard-bucket-usage.json`

Dashboard sync script (keeps namespace/user/bucket variable definitions aligned with ACL source of truth):

- `deployments/abc-nodes/nomad/sync-grafana-definitions.sh`

Dashboard UID:

- `abc-nodes-usage-overview`
- `abc-nodes-bucket-usage`

Before redeploying Grafana after ACL/user/bucket changes, run:

```bash
bash deployments/abc-nodes/nomad/sync-grafana-definitions.sh
abc admin services nomad cli -- job run deployments/abc-nodes/nomad/grafana.nomad.hcl
```

Or use the wrapper (sync + `job run`):

```bash
bash deployments/abc-nodes/nomad/scripts/redeploy-grafana-dashboards.sh
```

Key usage views include:

- Capacity snapshot (CPU/memory allocated/unallocated)
- Group fairness and queue pressure by namespace
- Efficiency indicators
- Research-user drilldowns
- Institute usage (derived from `script-job-<institute>-...` naming)
- Research-group usage derived directly from namespace

**CPU cores panels:** usage overview converts Nomad MHz gauges to approximate **cores** using valid PromQL `group_left()` (see observability doc). Invalid `group_left (expr …)` breaks parsing and panels stay empty.

## Test Expectations

- Unit tests (HCL generation and command behavior) should pass:
  - `go test ./internal/hclgen/job/... ./cmd/job/...`
- Live integration-style test runs should produce:
  - Successful stress-ng and hyperfine completion in target namespaces
  - Corresponding Prometheus/Loki signals visible in Grafana usage panels

## Operational Notes

- Use `ABC_ACTIVE_CONTEXT=aither-bootstrap` (or equivalent admin context) for namespace admin and service job deploys.
- If submitting to protected namespaces, verify ACL token permissions first.
- If dashboards fail provisioning, inspect Grafana allocation status and template errors via Nomad alloc events/logs.

## MinIO Bucket Automation

Use the automation script below to create namespace-matched buckets, user folder policies, group-admin bucket policies, and a cluster-admin all-data policy:

- `deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh`

Behavior:

- Creates one bucket per namespace for:
  - `su-mbhg-bioinformatics`
  - `su-mbhg-hostgen`
  - `su-mbhg-animaltb`
  - `su-psy-neuropsychiatry`
  - `su-sdsct-ceri`
- Creates folder placeholders:
  - `shared/.keep`
  - `users/<user>/.keep` (for each configured user)
  - `shared/<user>/.keep` (per-user collaboration prefix)
- Generates policy JSON files under:
  - `deployments/abc-nodes/acl/minio-policies/generated`
- Policy model (member users):
  - **Private:** `users/<user>/*` — read/write/delete for that user only
  - **Shared:** `shared/<user>/*` — read/write/delete for that user; other members typically have read on broader `shared/*` per generated JSON
  - **Listing:** `s3:ListBucket` restricted so only `users/` and `shared/` prefixes appear for members
  - `ns-<namespace>-group-admin`: full object access to that namespace bucket
  - `cluster-admin-all-namespace-data`: full access to all namespace buckets
- Automatically attaches cluster-admin policy to:
  - MinIO root/admin account used for bootstrap (`MINIO_USER`)
  - Optional existing users listed in `CLUSTER_ADMIN_USERS`

Run:

```bash
MINIO_USER=<root-user> MINIO_PASS=<root-pass> \
bash deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh
```

Optional: auto-create a dedicated cluster-admin IAM principal and attach global policy:

```bash
MINIO_USER=<root-user> MINIO_PASS=<root-pass> \
CLUSTER_ADMIN_IAM_USER=abc-cluster-admin \
CLUSTER_ADMIN_IAM_PASS='<strong-password>' \
bash deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh
```

Credential persistence (recommended):

- Local snapshot (gitignored):
  - `deployments/abc-nodes/acl/minio-credentials.env`
- Nomad variables (default enabled):
  - namespace: `abc-services` (override with `NOMAD_IAM_NAMESPACE`)
  - path prefix: `nomad/jobs/abc-nodes-minio-iam/<principal>`
- Vault KV v2 (optional):
  - enable with `SYNC_VAULT=1` and `VAULT_TOKEN=<token>`
  - default mount/prefix: `secret/abc-nodes/minio-iam/<principal>`

Example with all sinks enabled:

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

Note:

- For Nomad variable sync, use an admin context with write access to **`abc-services`** Nomad variables (for example `ABC_ACTIVE_CONTEXT=abc-cluster-admin`).
