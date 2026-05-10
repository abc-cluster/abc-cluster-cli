---
sidebar_position: 3
---

# auth context / config

Manage cluster contexts and local configuration.

> **Relocated 2026-05-08:** `abc context ...` was moved under `abc auth context ...`.
> The root-level `abc context` is kept as a deprecated alias for one release —
> it prints a one-line stderr note and forwards to the new location. Update
> scripts to `abc auth context ...` at your convenience.

## auth context add

Create a new named context and optionally make it active:

```bash
abc auth context add <name> \
  --url https://api.abc-cluster.io \
  --access-token <token> \
  --workspace <workspace-id> \
  [--region <region>] \
  [--org <org-id>]
```

## auth context list

```bash
abc auth context list
```

## auth context use

Switch the active context:

```bash
abc auth context use <name>
# or via env var (one-shot override):
ABC_ACTIVE_CONTEXT=dev abc job list
```

## auth context remove

```bash
abc auth context remove <name>
```

## config init

Create `~/.abc/config.yaml` with a blank `default` context:

```bash
abc config init
```

## config set / get / list / unset

```bash
abc config set active_context dev
abc config get active_context
abc config list
abc config unset active_context
```

Common config keys:

| Key | Description |
|---|---|
| `active_context` | Default context name |
| `contexts.<name>.url` | API endpoint |
| `contexts.<name>.access_token` | Access token |
| `contexts.<name>.workspace_id` | Workspace ID |
| `contexts.<name>.controller_url` | abc-controller-svc endpoint for the capability probe (optional; when set, `abc cluster capabilities sync` probes here instead of Nomad) |
| `contexts.<name>.cluster_type` | `abc-nodes` \| `abc-grove` — used by tier-default capability seeding |
