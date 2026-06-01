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

**Opt into the broker pathway** — claim a slot whose returned config
contains only an opaque token + cluster endpoint (no real Nomad / MinIO
secrets on the laptop):

```bash
abc auth claim <CODE> --cred-source=seedling/v1 \
  --email you@sun.ac.za --name "Your Name" --consent
```

See [Credential-resolution modes](#credential-resolution-modes-cred_source) below.

## auth config refresh

Re-pull the active slot's config from the cluster's auth service. Use
this after an admin rotates your credentials, on a fresh laptop, or to
recover from a local-edit mishap. Other contexts in your config are
preserved — only the active context is replaced.

```bash
abc auth config refresh             # write to disk
abc auth config refresh --dry-run   # show what would change
abc auth config refresh --diff      # print server-returned YAML
abc auth config refresh --auth-endpoint https://auth.example.com
```

Auth: the active context's `access_token` is sent as `Authorization: Bearer`
to `GET /slots/me/config` on the cluster auth service. The auth-svc URL is
derived from the active context's cluster endpoint (e.g.
`https://nomad.seedling.abc-cluster.cloud` → `https://auth.seedling.abc-cluster.cloud`);
override with `--auth-endpoint` if the convention does not match.

## Credential-resolution modes (`cred_source`)

Each context in `~/.abc/config.yaml` carries a top-level `cred_source`
field that selects how `abc` resolves the real Nomad ACL token + MinIO
S3 credentials on each invocation:

| `cred_source` value | What's on disk | How creds are obtained | When to use |
|---|---|---|---|
| `""` / `local` (default) | Real Nomad token + MinIO `secret_key` baked into the YAML | Read directly from `admin.services.{nomad,minio}` fields — zero network IO | Trusted laptop, fast offline operation, today's default |
| `seedling/v1` | Only an opaque token (`abco_…`) + identity + cluster endpoint | CLI POSTs the opaque to the broker (`/auth/exchange`) on first use; result cached in-memory for 5 minutes | Higher security posture: real Nomad/MinIO secrets never touch disk; instantly revocable via admin flip |

**Opt in at claim time** — pass `--cred-source=seedling/v1` to `abc auth claim`
(or have your group's `cred_source_default` set server-side).

**Flip an existing slot** — operators flip via the admin endpoint; the
user then re-runs `abc auth config refresh` to pull the new opaque-shape blob.

**Revocation** — when an admin flips a slot back to `local` (or revokes it
outright), the opaque immediately stops authenticating. The next CLI call
prints `abc: cred_source="seedling/v1" broker resolve failed: …
invalid_or_inactive_token` on stderr.

**Retry behaviour** — transient broker failures (5xx, 429, connection
drops) are retried with exponential backoff up to 3 attempts;
non-retryable refusals (4xx other than 429) fail immediately so admin
revocations and configuration errors surface to the user without retry
latency. Tunables: `BrokerRetryMaxAttempts=3`,
`BrokerRetryMinBackoff=200ms`, `BrokerRetryMaxBackoff=2s`.

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
