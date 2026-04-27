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

## Environment-variable auth

All token values can be passed via env var instead of stored config:

```bash
ABC_ACCESS_TOKEN=<token> abc auth whoami
```

This is useful in CI/CD pipelines where you don't want to commit credentials.
