---
title: Phase 4 — Deploy the landing page
---

# Phase 4 — Deploy the landing page

Two things need to be running before users can claim access:

1. The **claim service** (`claim_server.py`) running as a Nomad job.
2. The **landing page** (static files) served from the cluster node.

Both come from the `abc-seedling-template` directory in `abc-deployments`.

## Step 1 — Deploy the claim service

### Prepare the Nomad job spec

Open `nomad-jobs/abc-landing-api.nomad.hcl` and update:

**Artifact source** — where `claim_server.py` will be fetched at job dispatch.
Host it alongside the landing files, or serve it from any reachable URL:

```hcl
artifact {
  source      = "https://your-cluster.example.com/claim_server.py"
  destination = "${NOMAD_TASK_DIR}"
  mode        = "file"
}
```

**Allowed domain** (optional) — restrict claims to an institutional email suffix:

```hcl
env {
  ALLOWED_DOMAIN = "your-institution.ac.za"  # or "" to accept any email
}
```

### Submit the job

```bash
abc job run nomad-jobs/abc-landing-api.nomad.hcl
```

Or directly with the Nomad CLI:

```bash
nomad job run \
  -address=https://nomad.your-cluster.example.com \
  -token=<management-token> \
  nomad-jobs/abc-landing-api.nomad.hcl
```

Verify the service is healthy:

```bash
curl http://your-node:8081/healthz
# {"status":"ok"}
```

The claim service listens on `localhost:8081`. It is not exposed directly
to the internet — only via the reverse proxy `/api/*` route (if you have
one) or directly via its port on a LAN.

> **Database path:** The job spec expects the SQLite database at
> `/data/abc-landing.db`. Make sure `provision.py` wrote (or you copied)
> the database there before submitting the job.

## Step 2 — Deploy the landing page

### Set configuration variables

```bash
# Required
export ABC_CLUSTER_HOSTNAME="your-cluster.example.com"
export ABC_VM_SSH_TARGET="your-ssh-alias"          # or user@host

# Cluster identity
export ABC_CLUSTER_NAME="Your Institution ABC Cluster"

# Optional: restrict claims to institutional email addresses
export ABC_ALLOWED_DOMAIN="your-institution.ac.za"  # "" = any email accepted

# Optional: banner shown below the header on the landing page
export ABC_BANNER_TEXT="Private cluster hosted at Your Institution"
```

Alternatively, write values into `.secrets/` files next to the template
directory (one value per file, filename = variable name without `ABC_`,
lowercased with hyphens):

```
.secrets/
  cluster-hostname
  cluster-name
  allowed-domain
  banner-text
  vm-ssh-target
```

### Dry-run first

Always run `--dry-run` before touching the VM to verify substitution:

```bash
bash scripts/deploy-landing.sh --dry-run
```

Inspect `/tmp/abc-landing-dry-run/` and confirm:

```bash
# Zero REPLACE_ tokens should remain
grep "REPLACE_" /tmp/abc-landing-dry-run/index.html   # no output = good

# Service URLs contain your hostname
grep "your-cluster.example.com" /tmp/abc-landing-dry-run/index.html | head -3
```

### Deploy

```bash
bash scripts/deploy-landing.sh
```

The script substitutes all `REPLACE_*` tokens in `index.html`, copies all
landing files to the VM via `scp`, and moves them into place. It refuses
to deploy if any `REPLACE_*` token remains unsubstituted.

### Vendored CSS

`chrome.css` and `hero.css` are vendored from `abc-site-kit`. If they are
missing, regenerate them:

```bash
bash scripts/vendor-brand.sh
```

## Step 3 — Verify end-to-end

With both the claim service and landing page live:

1. Open the landing page URL in a browser — cluster name should appear in the title.
2. If configured, the banner should appear below the header.
3. Fill in the claim form with a valid code from the pool (name + email + code).
4. Credentials should appear, and the `config.yaml` download button should produce
   a valid YAML file.
5. Submit the same code again — should fail with `code_invalid_or_used`.

## Step 4 — Deploy the documentation

The `/docs/` path is served separately. To build and deploy the CLI docs:

```bash
bash scripts/deploy-docs.sh
```

This builds the Docusaurus tier site and copies it to the node under `/docs/`.

## Next step (optional)

→ [Reverse proxy / TLS](caddy) — add Caddy for internet-facing clusters
