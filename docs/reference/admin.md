---
sidebar_position: 9
---

# admin services

Proxy into Nomad, Vault, Consul, Pulumi, Terraform, and other service CLIs without manual token wrangling. `abc` resolves credentials from the active context and injects them as environment variables.

## Patterns

There are two equivalent forms. Use whichever reads more naturally.

### Unified dispatcher (preferred)

```bash
abc admin services cli <tool> [--] [tool-args...]
```

The tool name comes first, then optional `--` to separate `abc` flags from upstream flags.

### Per-service form

```bash
abc admin services <tool> cli -- <upstream-args...>
```

The original form — still fully supported. Both forms inject identical credentials.

### Available tools

| Tool | Binary | Credentials injected |
|------|--------|---------------------|
| `nomad` | `nomad` | `NOMAD_ADDR`, `NOMAD_TOKEN`, `NOMAD_NAMESPACE` |
| `nomad-pack` | `nomad-pack` | `NOMAD_ADDR`, `NOMAD_TOKEN`, `NOMAD_NAMESPACE` |
| `terraform` | `terraform` | `NOMAD_ADDR`, `NOMAD_TOKEN`, `TF_VAR_nomad_*`, `TF_VAR_<extra>` |
| `pulumi` | `pulumi` | `NOMAD_ADDR`, `NOMAD_TOKEN`, `MINIO_SERVER/USER/PASSWORD`, `PULUMI_ACCESS_TOKEN`, `PULUMI_CONFIG_PASSPHRASE` |
| `minio` | `mcli` / `mc` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_ENDPOINT_URL`, `MINIO_ROOT_*` |
| `vault` | `vault` / `bao` | `VAULT_ADDR`, `VAULT_TOKEN` |
| `loki` | `logcli` | `LOKI_ADDR` |
| `rustfs` | `rustfs` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_ENDPOINT_URL` |
| `boundary` | `boundary` | — |
| `consul` | `consul` | — |
| `tailscale` | `tailscale` | — |
| `traefik` | `traefik` | — |
| `hashi-up` | `hashi-up` | — |
| `rclone` | `rclone` | — |
| `eget` | `eget` | — |
| `nebula` | `nebula` | — |
| `ntfy` | `ntfy` | — |
| `grafana` | `grafana-cli` | — |
| `postgres` | `psql` | — |

## Credential source selector (`--config`)

Most service passthrough commands accept a leading `--config <backend>`
flag (defaults to `local`) that selects which cred_source map is consulted:

| `--config` | Source of credentials |
|---|---|
| `local` *(default)* | inline `admin.services.<svc>.cred_source.local.*` (or top-level `<field>` fallback) |
| `nomad` | resolved live from `nomad+var@<ns>/<path>#<key>` references at `admin.services.<svc>.cred_source.nomad.*` |
| `vault` | resolved live from `vault+kv2@<mount>/data/<path>#<key>` references at `admin.services.<svc>.cred_source.vault.*` |

**Precedence interaction with shell env vars:**

- `--config local` (default): shell-set env vars (e.g. `MINIO_ROOT_USER` in
  your environment) take precedence over the config-resolved value. This is
  the usual override semantics.
- `--config nomad` / `--config vault`: the explicit selector is
  authoritative. Shell-set values that disagree are **ignored** and a
  one-time warning is emitted citing the selector.

To preview which value would win for any tool/selector combination without
running the tool, see [env-var introspection](#env-var-introspection) below.

## Nomad

```bash
abc admin services cli nomad -- job status
abc admin services cli nomad -- job status abc-nodes-grafana
abc admin services cli nomad -- job run -detach \
    deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services cli nomad -- alloc logs <alloc-id>
```

Set the active context first: `export ABC_CLI_CONTEXT=abc-bootstrap`

## Pulumi

Pulumi credentials and the project working directory are resolved from `admin.services.pulumi` in the active context:

```bash
abc admin services cli pulumi -- stack ls
abc admin services cli pulumi -- up --yes
abc admin services cli pulumi -- destroy --yes
abc admin services cli pulumi -- stack output --json
```

Override Nomad credentials for a single invocation:

```bash
abc admin services cli --nomad-addr http://100.77.21.36:4646 pulumi -- up --yes
```

## Terraform

```bash
abc admin services cli terraform -- init
abc admin services cli terraform -- plan
abc admin services cli terraform -- apply -auto-approve
```

Extra `TF_VAR_*` overrides are read from `admin.services.terraform.vars` in the active context.

## Vault

```bash
abc admin services cli vault -- kv get secret/myapp/config
abc admin services cli vault -- token lookup
```

## Consul

```bash
abc admin services cli consul -- catalog services
abc admin services cli consul -- health state passing
```

## Tailscale

```bash
abc admin services cli tailscale -- status
abc admin services cli tailscale -- ping <node>
```

## RustFS

```bash
abc admin services cli rustfs -- ls
```

## CLI setup

Bootstrap upstream CLI binaries if not already installed:

```bash
abc admin services cli setup --all
abc admin services cli setup --service nomad
```

Check which managed binaries are available:

```bash
abc admin services cli status
```

## cluster commands

```bash
abc cluster capabilities sync   # pull cluster capabilities to local config
abc cluster capabilities show   # display current capabilities
```

## Env-var introspection

`abc admin env` exposes the CLI's env-var resolution surface so operators can
debug "why is the wrong cluster being hit?" / "is my shell var being shadowed?"
without running the actual command.

```bash
abc admin env list                 # every canonical env var, grouped by bucket
abc admin env list --bucket abc-api
abc admin env show <NAME>          # full precedence walk for one variable
abc admin env validate             # detect forbidden patterns, vendor leaks,
                                   # and shadowed values
```

### `abc admin env list`

Groups every registry entry by bucket (`abc-api`, `abc-cli`, `abc-resource`,
`abc-component`, `tool-binary`, `debug-test`, `vendor-fallback`,
`subprocess-out`). Each row shows whether the variable is currently set and
which source won; secrets are redacted to `***`.

### `abc admin env show <NAME>`

Walks the full precedence ladder (flag → ABC env → vendor env → context →
default) and reports the winning source. For variables with shadowing
relationships, prints a per-selector breakdown comparing what each
`--config local | nomad | vault` choice would resolve to. Reference strings
(`nomad+var@…`, `vault+kv2@…`) are shown unredacted — they describe **where**
the secret lives, not the secret itself; only literal secret values get
redacted.

Example:

```text
$ MINIO_USER=alice abc admin env show MINIO_USER
Name:     MINIO_USER
Bucket:   subprocess-out
Purpose:  user for Pulumi MinIO provider
Source:   abc-env  (value: alice)
Shadowing:
  --config local → contexts.<n>.admin.services.minio.cred_source.local.user
                   (or top-level .user) = (not set)  shell 'alice' wins
  --config nomad → contexts.<n>.admin.services.minio.cred_source.nomad.user
                   = (not configured)
  --config vault → contexts.<n>.admin.services.minio.cred_source.vault.user
                   = (not configured)
```

When the config has a disagreeing value, the diff is reported inline:

```text
$ MINIO_USER=alice abc admin env validate
WARN: MINIO_USER='alice' in shell; contexts.<n>.admin.services.minio.cred_source.local.user='bob'
      --config local → 'alice' wins (shell)
      --config nomad → resolved from Nomad Variable; shell ignored, warning emitted
      --config vault → (not configured)
```

### `abc admin env validate`

Exits non-zero when forbidden patterns are present in the environment:

- `ABC_DISABLE_*` (use `ABC_<SCOPE>_NO_<FEATURE>`)
- `ABC_*_OFF` (same)
- `ABC_GROVE_*` / `ABC_SEEDLING_*` / `ABC_CLOUD_*` (env vars are tier-neutral)

Emits non-fatal warnings for:

- Unknown `ABC_*` names (likely typos or legacy)
- Vendor-fallback use (`NOMAD_ADDR` etc.) while an ABC context is configured
- Shadowed env vars where shell and config disagree

## See also

- [Environment variables reference](./env-vars.md) — full registry of every
  env var the CLI knows about, generated from
  `internal/envvars/registry.go`. The canonical source of truth.
- [Global flags](./global-flags.md) — flag → env-var → config mapping for the
  researcher-facing surface.
