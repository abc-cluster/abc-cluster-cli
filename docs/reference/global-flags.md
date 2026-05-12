---
sidebar_position: 2
---

# Global flags

Available on every `abc` command.

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--address` | `ABC_API_ADDRESS` | `https://api.abc-cluster.io` | abc-cluster API endpoint |
| `--access-token` | `ABC_API_TOKEN` | *(unset)* | abc-cluster access token |
| `--workspace` | `ABC_WORKSPACE` | *(user default)* | Workspace ID |
| `--region` | `ABC_REGION` | *(unset)* | Sovereignty region (ZA / KE / MZ / …) |
| `--namespace` | `ABC_NAMESPACE` | *(unset)* | Logical namespace within workspace |
| `--org` | `ABC_ORG` | *(unset)* | Organization ID (multi-org) |
| `--cluster` | `ABC_CLUSTER` | *(unset)* | Target named cluster |
| `--output` | `ABC_CLI_OUTPUT_FORMAT` | `table` | Output format: `table` / `json` / `yaml` |
| `-q` / `--quiet` | `ABC_CLI_QUIET` | `false` | Suppress informational stderr |
| `--debug[=N]` | `ABC_CLI_DEBUG` | `0` | Write structured JSON debug log |
| `--sudo` | `ABC_CLI_SUDO` | `false` | Cluster-admin elevation |
| `--cloud` | `ABC_CLI_CLOUD_MODE` | `false` | Infrastructure elevation |
| `--exp` | `ABC_CLI_EXP_MODE` | `false` | Enable experimental features |
| `--user <email>` | `ABC_API_AS_USER` | *(unset)* | Act on behalf of user (admin only) |
| *(env only)* | `ABC_CLI_CONTEXT` | *(unset)* | One-shot override of active context |
| *(env only)* | `ABC_CLI_CONFIG_FILE` | `~/.abc/config.yaml` | Override config-file location |
| *(env only)* | `ABC_CLI_DISABLE_UPDATE_CHECK` | *(unset)* | Silence update notifications |
| *(env only)* | `ABC_CLI_AUTOMATION` | *(unset)* | `=1` signals non-interactive automation context |

The full env-var surface (~76 entries across `ABC_API_*`, `ABC_CLI_*`,
`ABC_<RESOURCE>`, component overrides, debug/test namespace, vendor
fallback, and subprocess injection) lives in [Environment variables](./env-vars.md) —
that page is generated directly from the CLI's registry, so it cannot
drift from the implementation.

## Resolution precedence

For every variable the CLI consults, the resolver walks:

```
flag  >  ABC env  >  vendor env  >  active context config  >  default
```

- **flag** — explicit `--<name>` on the command line
- **ABC env** — the canonical `ABC_*` name in your shell (uses `os.LookupEnv`,
  so an explicit empty `ABC_API_ADDRESS=` overrides the context config)
- **vendor env** — last-resort fallback for a handful of ABC vars with a
  vendor namesake (e.g. `ABC_REGION` falls back to `NOMAD_REGION`). Emits a
  one-time warning when no ABC context is configured.
- **active context** — the value persisted under `contexts.<name>.<key>` in
  `~/.abc/config.yaml`
- **default** — registry-declared default (or empty)

Use `abc admin env show <NAME>` to see which source won for any variable in
your current shell.

## Elevation tiers

| Tier | Flag | Scope |
|---|---|---|
| Standard | *(none)* | Researcher operations |
| Sudo | `--sudo` | Cluster-admin write ops, node management |
| Cloud | `--cloud` | Infrastructure, accounting, compliance |

## Debug logging

`--debug` (or `ABC_CLI_DEBUG=1`) writes a structured JSON log to stderr.
`--debug=2` adds verbose HTTP request/response traces.

## Active context

`abc` resolves the active context from, in order:

1. `ABC_CLI_CONTEXT` env var
2. `active_context` key in `~/.abc/config.yaml`
3. The `default` context (fallback)

## Env-var introspection

Three operator commands surface the env-var surface live against your current
shell + active context:

```bash
abc admin env list                 # group every canonical env var by bucket
abc admin env show ABC_API_TOKEN   # full precedence walk for one variable
abc admin env validate             # detect forbidden patterns, vendor leaks,
                                   # and shadowed values
```

See [admin env](./admin.md#env-var-introspection) for details.
