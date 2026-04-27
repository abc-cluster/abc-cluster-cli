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

## Nomad

```bash
abc admin services cli nomad -- job status
abc admin services cli nomad -- job status abc-nodes-grafana
abc admin services cli nomad -- job run -detach \
    deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services cli nomad -- alloc logs <alloc-id>
```

Set the active context first: `export ABC_ACTIVE_CONTEXT=abc-bootstrap`

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
