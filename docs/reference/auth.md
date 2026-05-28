---
sidebar_position: 4
---

# auth

Interactive and token-based authentication.

## auth login

Interactive first-time setup — prompts for endpoint, credentials, and workspace:

```bash
abc auth login
```

## auth logout

Clear stored tokens for the active context:

```bash
abc auth logout
```

## auth whoami

Print the authenticated identity and active context:

```bash
abc auth whoami
```

## auth token

Print the current access token (useful for piping into other tools):

```bash
abc auth token
```

## auth refresh

Force a token refresh:

```bash
abc auth refresh
```

## auth claim

Redeem a claim code into a ready-to-use config. The signup service draws
an unclaimed slot from the code's group, marks it claimed, and returns a
complete `config.yaml`; this command installs it at `~/.abc/config.yaml`
(or `ABC_CLI_CONFIG_FILE`) and makes it the active context — equivalent
to running `abc auth context add --from-file` on a downloaded YAML, but
in one step.

```bash
# Minimal — uses --tier=seedling, prompts for POPIA consent
abc auth claim <CODE> --email you@sun.ac.za --name "Your Name"

# Skip the interactive consent prompt (policy already read)
abc auth claim <CODE> --email you@sun.ac.za --name "Your Name" --consent

# Blind-pool draw (no code; server picks a free slot in the group)
abc auth claim --group-name demo --email you@sun.ac.za --name "Your Name"

# Read the code from stdin (keeps it out of shell history)
echo "$CLAIM_CODE" | abc auth claim --code-stdin \
  --email you@sun.ac.za --name "Your Name" --consent
```

Endpoint resolution: `--endpoint URL` wins; otherwise `--tier <name>`
composes `https://signup.<tier>.abc-cluster.cloud/claim` (default tier
`seedling`). Other flags: `--as <name>` (rename the imported context),
`--force` (overwrite an existing context), `--print-only` (emit the YAML
to stdout instead of writing to disk).

## auth context

Manage saved authentication contexts (subgroup; relocated from root-level
`abc context` on 2026-05-08).

```bash
abc auth context list
abc auth context show
abc auth context use <name>
abc auth context add <name> --url <api> --access-token <token>
abc auth context delete <name>
```

See [auth context / config](./auth-context.md) for the full reference.

## Environment-variable auth

All token values can be passed via env var instead of stored config:

```bash
ABC_API_TOKEN=<token> abc auth whoami
```

This is useful in CI/CD pipelines where you don't want to commit credentials.
