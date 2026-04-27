---
sidebar_position: 2
---

# Global flags

Available on every `abc` command.

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--url` | `ABC_API_ENDPOINT` | `https://api.abc-cluster.io` | API endpoint URL |
| `--access-token` | `ABC_ACCESS_TOKEN` | *(unset)* | Access token |
| `--workspace` | `ABC_WORKSPACE_ID` | *(user default)* | Workspace ID |
| `--cluster` | `ABC_CLUSTER` | *(unset)* | Target named cluster |
| `-q` / `--quiet` | | `false` | Suppress informational stderr |
| `--debug[=N]` | `ABC_DEBUG` | `0` | Write structured JSON debug log |
| `--sudo` | `ABC_CLI_SUDO_MODE` | `false` | Cluster-admin elevation |
| `--cloud` | `ABC_CLI_CLOUD_MODE` | `false` | Infrastructure elevation |
| `--exp` | `ABC_CLI_EXP_MODE` | `false` | Enable experimental features |
| `--user <email>` | `ABC_AS_USER` | *(unset)* | Act on behalf of user (admin only) |
| *(env only)* | `ABC_CLI_DISABLE_UPDATE_CHECK` | *(unset)* | Silence update notifications |

## Elevation tiers

| Tier | Flag | Scope |
|---|---|---|
| Standard | *(none)* | Researcher operations |
| Sudo | `--sudo` | Cluster-admin write ops, node management |
| Cloud | `--cloud` | Infrastructure, accounting, compliance |

## Debug logging

`--debug` (or `ABC_DEBUG=1`) writes a structured JSON log to stderr.  
`--debug=2` adds verbose HTTP request/response traces.

## Active context

`abc` resolves the active context from, in order:

1. `ABC_ACTIVE_CONTEXT` env var
2. `active_context` key in `~/.abc/config.yaml`
3. The `default` context (fallback)
