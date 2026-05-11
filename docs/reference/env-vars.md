---
sidebar_position: 99
---

<!--
  GENERATED FILE — DO NOT EDIT BY HAND.
  Source: internal/envvars/registry.go
  Regenerate: go generate ./internal/envvars/... (or "just gen")
  Drift check: "just gen-check"
-->

# Environment variables

The abc CLI uses a single registry-backed resolver for every environment
variable it consults. Names are scope-prefixed so the surface stays
predictable:

- `ABC_API_*` — transport, auth, identity (talks to the abc-cluster
  control plane)
- `ABC_CLI_*` — local CLI configuration, behaviour, output, debug
- `ABC_<COMPONENT>_*` — component-scoped overrides
  (controller, khan, node-probe, tool binaries)
- `ABC_<RESOURCE>` — cluster-resource selectors only:
  `ABC_WORKSPACE`, `ABC_REGION`, `ABC_NAMESPACE`,
  `ABC_ORG`, `ABC_CLUSTER`, `ABC_PROJECT`,
  `ABC_INVESTIGATION`

## Resolution precedence

For any registered variable, the resolver walks:

```
flag  >  ABC env  >  vendor env  >  active context config  >  default
```

- **flag** — explicit `--<name>` on the command line
- **ABC env** — `ABC_*` set in the environment (via `os.LookupEnv` —
  explicit empty counts)
- **vendor env** — last-resort fallback for a handful of ABC vars that
  have a vendor namesake (e.g. `ABC_REGION` falls back to
  `NOMAD_REGION`). Emits a one-time warning when no ABC context is
  configured.
- **active context** — value persisted under `contexts.<name>.<key>`
  in `~/.abc/config.yaml`
- **default** — registry-declared default (or empty)

Use `abc admin env show <NAME>` to see which source won for any
variable in your current shell.

## Variables by scope


### abc-cluster API

Transport and auth for the abc-cluster control plane.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_API_ADDR` | abc-cluster API endpoint (controller / API gateway) | `--address` | `url` | — | — |
| `ABC_API_TOKEN` 🔒 | bearer token for abc-cluster API | `--access-token` | `access_token` | — | — |
| `ABC_API_AS_USER` | operator-only: impersonate another user (sent as identity-override header) | — | — | — | — |


### CLI configuration & behaviour

Local CLI state: config file location, output format, telemetry, modes.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_CLI_CONTEXT` | one-shot override of active_context for this invocation | — | — | — | — |
| `ABC_CLI_CONFIG_FILE` | override ~/.abc/config.yaml location | — | — | — | — |
| `ABC_CLI_CACHE_DIR` | override ~/.abc/cache/ location | — | — | — | — |
| `ABC_CLI_OUTPUT_FORMAT` | default output format: table \| json \| yaml | `--output` | `output_format` | — | `table` |
| `ABC_CLI_SUDO` | operator passthrough mode: =1 unlocks abc sudo <vendor> commands | — | — | — | — |
| `ABC_CLI_CLOUD_MODE` | =1 unlocks abc-cloud control-plane commands (operator only) | — | — | — | — |
| `ABC_CLI_EXP_MODE` | =1 enables experimental CLI commands | — | — | — | — |
| `ABC_CLI_NO_TELEMETRY` | =1 disables CLI-side telemetry (controller telemetry is separate) | — | — | — | — |
| `ABC_CLI_QUIET` | =1 suppresses non-essential stderr output | — | — | — | — |
| `ABC_CLI_AUTOMATION` | =1 signals non-interactive automation context (suppresses prompts) | — | — | — | — |
| `ABC_CLI_USE_EGET` | eget preference: auto \| 0/false/no/off (never use eget) | — | — | — | `auto` |
| `ABC_CLI_TMPDIR` | override temp dir for CLI staging (uploads, generated scripts, ...) | — | — | — | — |
| `ABC_CLI_ASSETS_DIR` | override location of CLI-bundled assets (templates, schemas, ...) | — | — | — | — |
| `ABC_CLI_BINARIES_DIR` | override managed-binaries dir (default: ~/.abc/bin) | — | — | — | — |


### Cluster resource selectors

Resource identifiers a command operates against.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_WORKSPACE` | workspace ID for this invocation | `--workspace` | `workspace_id` | — | — |
| `ABC_REGION` | sovereignty region (ZA / KE / MZ / ...) | `--region` | `region` | `NOMAD_REGION` | — |
| `ABC_NAMESPACE` | logical namespace within workspace | `--namespace` | `namespace` | `NOMAD_NAMESPACE` | — |
| `ABC_ORG` | organization ID when multi-org | `--org` | `org_id` | — | — |
| `ABC_CLUSTER` | cluster identifier within active context | `--cluster` | — | — | — |
| `ABC_PROJECT` | project identifier within workspace | `--project` | — | — | — |
| `ABC_INVESTIGATION` | investigation identifier within project | `--investigation` | — | — | — |


### Component-scoped

Component overrides for capability cache, upload, crypt, node bootstrap.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_CAPABILITY_TTL` | capability cache TTL (e.g. 24h) | — | — | — | — |
| `ABC_CAPABILITY_HARD_EXPIRY` | capability hard-expiry window beyond TTL (e.g. 72h) | — | — | — | — |
| `ABC_UPLOAD_ENDPOINT` | tusd upload endpoint override | — | `upload_endpoint` | — | — |
| `ABC_UPLOAD_TOKEN` 🔒 | tusd upload token override | — | `upload_token` | — | — |
| `ABC_CRYPT_PASSWORD` 🔒 | rclone-crypt password (also keys abc secrets) | — | — | — | — |
| `ABC_CRYPT_SALT` 🔒 | rclone-crypt salt | — | — | — | — |
| `ABC_NODE_PASSWORD` 🔒 | abc-node bootstrap password | — | — | — | — |
| `ABC_NODE_NO_PROBE` | =1 skips the periodic node-probe sysbatch job | — | — | — | — |


### Tool binary overrides

Paths to subprocess binaries the CLI shells out to. Operator territory.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_NOMAD_BIN` | override path to the nomad binary | — | — | — | — |
| `ABC_VAULT_BIN` | override path to the vault binary | — | — | — | — |
| `ABC_RCLONE_BIN` | override path to the rclone binary | — | — | — | — |
| `ABC_MC_BIN` | override path to the mc (MinIO Client) binary | — | — | — | — |
| `ABC_S5CMD_BIN` | override path to the s5cmd binary | — | — | — | — |
| `ABC_NEXTFLOW_BIN` | override path to the nextflow binary | — | — | — | — |
| `ABC_NODE_PROBE_BIN` | override path to the abc-node-probe binary | — | — | — | — |
| `ABC_SHELLCHECK_BIN` | override path to the shellcheck binary | — | — | — | — |
| `ABC_EGET_BIN` | override path to the eget binary | — | — | — | — |


### Debug & test (internal)

Reserved namespace. Not for end-user use.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `ABC_CLI_DEBUG` | =1 (or =N) enables CLI debug logging at level N | — | — | — | — |
| `ABC_CLI_DEBUG_KEEP_SCRIPT` | internal: keep generated scripts on disk after run | — | — | — | — |
| `ABC_CLI_LOG_LEVEL` | CLI log level: trace \| debug \| info \| warn \| error | — | — | — | `info` |
| `ABC_CLI_TRACE` | =1 enables CLI execution tracing | — | — | — | — |
| `ABC_CLI_TEST_NS` | internal: namespace override for integration tests | — | — | — | — |
| `ABC_CLI_TEST_TIMEOUT` | internal: timeout override for integration tests | — | — | — | — |
| `ABC_CLI_INTEGRATION_LOKI_REQUIRE` | internal: integration test requires Loki present | — | — | — | — |
| `ABC_CLI_INTEGRATION_LOKI_WAIT_SEC` | internal: integration test Loki wait timeout (seconds) | — | — | — | — |
| `ABC_CLI_INTEGRATION_OBS_STACK` | internal: integration test observability stack flag | — | — | — | — |
| `ABC_CLI_INTEGRATION_STRESS_NG` | internal: stress-ng integration test toggle | — | — | — | — |
| `ABC_CLI_INTEGRATION_STRESS_TIMEOUT` | internal: stress-ng integration test timeout | — | — | — | — |


### Vendor fallback (last resort)

Vendor env vars read silently when the canonical ABC name and active context are both unset. Emits a one-time warning if no ABC context exists.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `NOMAD_ADDR` | fallback: consulted only when no ABC context is configured | — | — | — | — |
| `NOMAD_TOKEN` 🔒 | fallback: consulted only when no ABC context is configured | — | — | — | — |
| `NOMAD_REGION` | fallback for ABC_REGION when ABC context lacks region | — | — | — | — |
| `NOMAD_NAMESPACE` | fallback for ABC_NAMESPACE when ABC context lacks namespace | — | — | — | — |
| `VAULT_ADDR` | fallback: consulted only in abc sudo vault passthrough | — | — | — | — |
| `VAULT_TOKEN` 🔒 | fallback: consulted only in abc sudo vault passthrough | — | — | — | — |


### Subprocess injection

The CLI constructs these for child processes from the active context. You do not set them.

| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
| `AWS_ACCESS_KEY_ID` | constructed for rclone/s5cmd/nextflow/mc subprocesses from active context | — | — | — | — |
| `AWS_SECRET_ACCESS_KEY` 🔒 | constructed for rclone/s5cmd/nextflow/mc subprocesses from active context | — | — | — | — |
| `AWS_ENDPOINT_URL` | constructed for rclone/s5cmd/nextflow/mc subprocesses from active context | — | — | — | — |
| `AWS_REGION` | constructed for rclone/s5cmd/nextflow/mc subprocesses from active context | — | — | — | — |
| `AWS_DEFAULT_REGION` | older vendor variant of AWS_REGION; some tools (older aws-cli, boto) only read this | — | — | — | — |
| `AWS_SESSION_TOKEN` 🔒 | session token for grove+ short-lived credentials (STS-style) | — | — | — | — |
| `AWS_CA_BUNDLE` | custom CA cert path for self-signed S3 endpoints (lab deployments) | — | — | — | — |
| `AWS_S3_FORCE_PATH_STYLE` | force path-style addressing; required for MinIO and RustFS | — | — | — | — |
| `S3_FORCE_PATH_STYLE` | older vendor variant of AWS_S3_FORCE_PATH_STYLE | — | — | — | — |
| `AWS_REQUEST_CHECKSUM_CALCULATION` | set to when_required for MinIO compatibility with newer AWS SDKs | — | — | — | — |
| `MC_HOST_local` 🔒 | MinIO Client connection alias for the active context; URL with embedded credentials | — | — | — | — |
| `MC_INSECURE` | MinIO Client: skip TLS verification (self-signed endpoints) | — | — | — | — |
| `MINIO_SERVER` | host:port for Pulumi MinIO provider (scheme stripped) | — | — | — | — |
| `MINIO_USER` | user for Pulumi MinIO provider | — | — | — | — |
| `MINIO_PASSWORD` 🔒 | password for Pulumi MinIO provider | — | — | — | — |
| `MINIO_ROOT_USER` | MinIO server root user (constructed for `abc admin services minio cli` passthrough) | — | — | — | — |
| `MINIO_ROOT_PASSWORD` 🔒 | MinIO server root password (constructed for `abc admin services minio cli` passthrough) | — | — | — | — |
| `RCLONE_CONFIG` | path to rclone config file (the CLI generates one per invocation) | — | — | — | — |



## Forbidden patterns

The registry rejects (and `abc admin env validate` flags) these
patterns in the environment:

- `ABC_DISABLE_*` — use `ABC_<SCOPE>_NO_<FEATURE>` instead
- `ABC_*_OFF` — same
- `ABC_GROVE_*` / `ABC_SEEDLING_*` / `ABC_CLOUD_*` —
  env vars are tier-neutral; they do not know which abc-cluster tier
  they're running against

## Subprocess injection

When the CLI shells out to `nomad`, `vault`, `rclone`,
`s5cmd`, `nextflow`, `mc`, or `pulumi`, it
**constructs** the relevant vendor env vars from the active context and
injects them into the child process. You do not need to set
`NOMAD_ADDR`, `AWS_ACCESS_KEY_ID` etc. in your shell —
the abstraction handles it. In `abc admin services <tool> cli`
passthrough commands, parent-shell vendor env vars are preserved so
operators can target alternate endpoints.

🔒 = redacted in `abc admin env list` output.


## Shadowing — alternate resolution paths

Some env vars have additional resolution paths beyond the standard
precedence ladder. A value you set in your shell may be **shadowed**
(replaced by a config-derived value) in specific contexts. Use
`abc admin env show <NAME>` to see which path is active for
any variable.

### `ABC_CRYPT_PASSWORD`

- abc secrets / abc data: contexts.<n>.crypt.password wins over the env var; stderr warning emitted on disagreement

### `ABC_CRYPT_SALT`

- abc secrets / abc data: contexts.<n>.crypt.salt wins over the env var; stderr warning emitted on disagreement

### `VAULT_ADDR`

- abc admin services vault cli: sourced from admin.services.vault.cred_source.local.http (or .nomad.* via --config nomad); shell env ignored when --config nomad

### `VAULT_TOKEN`

- abc admin services vault cli: sourced from admin.services.vault.cred_source.local.access_key (or .nomad.* via --config nomad); shell env ignored when --config nomad

### `AWS_ACCESS_KEY_ID`

- abc admin services minio cli --config nomad: sourced from admin.services.minio.cred_source.nomad.access_key (or .user); shell env ignored
- abc admin services minio cli --config vault: sourced from admin.services.minio.cred_source.vault.access_key; shell env ignored
- abc admin services rustfs cli --config nomad/vault: same pattern via admin.services.rustfs.cred_source.*

### `AWS_SECRET_ACCESS_KEY`

- abc admin services minio cli --config nomad: sourced from admin.services.minio.cred_source.nomad.secret_key (or .password); shell env ignored
- abc admin services minio cli --config vault: sourced from admin.services.minio.cred_source.vault.secret_key; shell env ignored

### `AWS_ENDPOINT_URL`

- abc admin services minio cli --config nomad/vault: sourced from admin.services.minio.cred_source.<sel>.endpoint; shell env ignored

### `MC_HOST_local`

- abc admin services minio cli --config nomad/vault: constructed from admin.services.minio.cred_source.<sel>.*; shell env ignored

### `MINIO_SERVER`

- abc admin services pulumi cli: sourced from admin.services.minio.cred_source.local.endpoint; shell env wins (--config local) or is replaced (--config nomad/vault)

### `MINIO_USER`

- abc admin services pulumi cli: sourced from admin.services.minio.cred_source.local.user; shell env wins (--config local) or is replaced (--config nomad/vault)

### `MINIO_PASSWORD`

- abc admin services pulumi cli: sourced from admin.services.minio.cred_source.local.password; shell env wins (--config local) or is replaced (--config nomad/vault)

### `MINIO_ROOT_USER`

- abc admin services minio cli: also reads admin.abc_nodes.minio_root_user as fallback when cred_source has no user field

### `MINIO_ROOT_PASSWORD`

- abc admin services minio cli: also reads admin.abc_nodes.minio_root_password as fallback when cred_source has no password field




_This page is generated from
[`internal/envvars/registry.go`](https://github.com/abc-cluster/abc-cluster-cli/blob/main/internal/envvars/registry.go)
— edits there propagate here via `go generate ./internal/envvars/...`._
