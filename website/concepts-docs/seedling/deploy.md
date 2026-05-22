---
title: Deploy landing + claim service
---

# Deploy landing + claim service

Two things need to be running before users can claim access:

1. The **claim service** (`claim_server.py`) running as a Nomad job.
2. The **landing page** (static files) served by a file server.

A reverse proxy (Caddy) is optional. On a private LAN you can serve files
directly; on an internet-facing deployment Caddy adds TLS automatically.

## 1. Deploy the claim service

### Edit the Nomad job spec

Open `nomad-jobs/abc-landing-api.nomad.hcl` and update two things:

**Artifact source** — where `claim_server.py` will be fetched from at
job dispatch time. The simplest option is to host it alongside the
landing page:

```hcl
artifact {
  source      = "https://your-cluster.example.com/claim_server.py"
  destination = "${NOMAD_TASK_DIR}"
  mode        = "file"
}
```

**Allowed domain** (optional) — restrict claims to institutional email addresses:

```hcl
env {
  ALLOWED_DOMAIN = "your-institution.ac.za"  # or "" to accept any email
}
```

### Submit the job

```bash
abc job run nomad-jobs/abc-landing-api.nomad.hcl
```

Or with the admin Nomad token directly:

```bash
nomad job run -address=https://nomad.your-cluster.example.com \
  -token=<admin-token> \
  nomad-jobs/abc-landing-api.nomad.hcl
```

Verify it's healthy:

```bash
curl https://your-cluster.example.com/healthz
# {"status":"ok"}
```

:::note Database path
The job spec expects the SQLite database at `/data/abc-landing.db`.
Make sure this path exists on the Nomad client node that runs the job,
and that `provision.py` wrote the database there (or copy it).
If you need a different path, edit the `--db` arg in the job spec before
submitting.
:::

## 2. Deploy the landing page

### Set configuration

Export the variables that `deploy-landing.sh` reads:

```bash
# Required
export ABC_CLUSTER_HOSTNAME="abc.your-institution.example.com"
export ABC_VM_SSH_TARGET="your-ssh-alias"          # or user@host

# Cluster identity
export ABC_CLUSTER_NAME="Your Institution ABC Cluster"
export ABC_ALLOWED_DOMAIN="your-institution.ac.za"  # "" = any email

# Banner (optional — shown below the header)
export ABC_BANNER_TEXT="Private cluster hosted at Your Institution"

# Supabase (leave empty to use claim-server.py instead)
export ABC_SUPABASE_URL=""
export ABC_SUPABASE_ANON_KEY=""
```

Alternatively, write values into `.secrets/` files next to the
`abc-seedling-template` directory (one value per file, filename = variable
name without the `ABC_` prefix, lowercase with hyphens):

```
.secrets/
  cluster-hostname
  cluster-name
  allowed-domain
  banner-text
  vm-ssh-target
```

### Dry-run first

Always run with `--dry-run` to verify substitution before touching the VM:

```bash
bash scripts/deploy-landing.sh --dry-run
```

Inspect the output in `/tmp/abc-landing-dry-run/`. Confirm:
- Zero `REPLACE_` tokens remain in `index.html`.
- Service URLs contain your cluster hostname.
- The banner text (if any) is present in the HTML.

```bash
grep "REPLACE_" /tmp/abc-landing-dry-run/index.html  # should print nothing
grep "your-cluster.example.com" /tmp/abc-landing-dry-run/index.html | head -3
```

### Deploy

```bash
bash scripts/deploy-landing.sh
```

The script:
1. Substitutes all five `REPLACE_*` tokens in `index.html`.
2. Copies `index.html`, `landing.js`, `style.css`, `chrome.css`, `hero.css`,
   `favicon.svg` to `/tmp/abc-landing-*` on the edge VM via `scp`.
3. Moves them to `$WWW_DIR` (default: `/var/www/<hostname>/`) on the VM.
4. Sets ownership to `caddy:caddy` (harmless if Caddy is not installed; change the owner if using a different server).

### Vendored CSS

`chrome.css` and `hero.css` are vendored from `abc-site-kit`. If they are
missing or stale, regenerate them:

```bash
bash scripts/vendor-brand.sh
```

## 3. Verify end-to-end

With both the claim service and landing page live:

1. Open `https://your-cluster.example.com/` in a browser.
2. The landing page should render with your cluster name in the title.
3. The banner (if configured) should appear below the header.
4. Fill in the claim form with a valid code from your pool:
   - Name + email + consent + claim code → submit.
   - Credentials (Nomad token, MinIO keys) should appear.
   - The `config.yaml` download button should produce a valid YAML file.
5. Try the same code again — should return 409 `code_invalid_or_used`.

## 4. Deploy the docs

The `/docs/` path is served separately from the landing page. To build and
deploy the CLI documentation:

```bash
bash scripts/deploy-docs.sh
```

This builds the Docusaurus tier site and copies it to
`/var/www/<hostname>/docs/` on the edge VM.

## Next step

→ [Reverse proxy / TLS](caddy) (optional)
