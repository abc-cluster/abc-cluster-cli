---
sidebar_position: 3
---

# context / config

Manage cluster contexts and local configuration.

## context add

Create a new named context and optionally make it active:

```bash
abc context add <name> \
  --url https://api.abc-cluster.io \
  --access-token <token> \
  --workspace <workspace-id> \
  [--region <region>] \
  [--org <org-id>]
```

## context list

```bash
abc context list
```

## context use

Switch the active context:

```bash
abc context use <name>
# or via env var (one-shot override):
ABC_ACTIVE_CONTEXT=dev abc job list
```

## context remove

```bash
abc context remove <name>
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
