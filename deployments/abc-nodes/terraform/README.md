# abc-nodes Terraform Deployment

This directory contains Terraform configuration for managing abc-nodes platform services on the Nomad cluster.

## Overview

The Terraform setup provides:
- **State management** - Track deployed services and detect configuration drift
- **Dependency ordering** - Ensure services deploy in correct order
- **Centralized configuration** - All variables in one place
- **Plan/apply workflow** - Preview changes before applying
- **Rollback capability** - Revert to previous states when needed

## Directory Structure

```
terraform/
├── provider.tf      # Nomad provider and backend configuration
├── variables.tf     # All configurable variables with defaults
├── main.tf          # Service deployment resources
├── outputs.tf       # Service endpoints and status outputs
├── .gitignore       # Ignore state files and sensitive data
└── README.md        # This file
```

## Prerequisites

1. **Terraform** - Install from [terraform.io](https://www.terraform.io/downloads)
   ```bash
   brew install terraform  # macOS
   ```

2. **Nomad credentials** - Export environment variables:
   ```bash
   export NOMAD_TOKEN="6acff123-f6eb-70c6-48d6-9650fdf2c45f"
   export NOMAD_ADDR="http://100.77.21.36:4646"
   ```

   Or use variables in `terraform.tfvars` (not recommended for security):
   ```hcl
   nomad_token   = "your-token-here"
   nomad_address = "http://100.77.21.36:4646"
   ```

## ⚠️ CRITICAL: Namespace Issue

**Before using Terraform, you MUST fix a namespace mismatch:**

The .nomad.hcl files declare various namespaces (`abc-services`, `services`, `abc-applications`) but **ALL services are actually running in the `default` namespace**. This will cause Terraform imports and plans to fail.

### Fix the Namespace Declarations

```bash
cd terraform
./fix-namespaces.sh
```

This script will:
1. Update all .nomad.hcl files to use `namespace = "default"`
2. Create `.bak` backups
3. Show you what changed

After running, review and commit the changes:
```bash
git diff ../nomad/
git add ../nomad/*.nomad.hcl
git commit -m "fix: correct namespace declarations to default"
```

## Quick Start

### 1. Initialize Terraform

```bash
cd terraform
terraform init
```

This downloads the Nomad provider and initializes the backend.

### 2. Validate Configuration

```bash
terraform validate
```

### 3. Preview Changes (Plan)

```bash
terraform plan
```

This shows what Terraform will create/modify/destroy **without actually doing it**.

### 4. Import Existing Services (First Time Only)

Since services are already running, import them into Terraform state:

```bash
# Core services
terraform import nomad_job.traefik abc-nodes-traefik
terraform import nomad_job.minio abc-nodes-minio
terraform import nomad_job.rustfs abc-nodes-rustfs
terraform import nomad_job.tusd abc-nodes-tusd
terraform import nomad_job.uppy abc-nodes-uppy
terraform import nomad_job.ntfy abc-nodes-ntfy
terraform import nomad_job.job_notifier abc-nodes-job-notifier
terraform import nomad_job.auth abc-nodes-auth

# Observability (if deploy_observability_stack = true)
terraform import 'nomad_job.prometheus[0]' abc-nodes-prometheus
terraform import 'nomad_job.loki[0]' abc-nodes-loki
terraform import 'nomad_job.grafana[0]' abc-nodes-grafana
terraform import 'nomad_job.alloy[0]' abc-nodes-alloy

# Optional services (if deploy_boundary_worker = true)
terraform import 'nomad_job.boundary_worker[0]' abc-nodes-boundary-worker

# Optional services (if deploy_optional_services = true)
terraform import 'nomad_job.docker_registry[0]' abc-nodes-docker-registry
```

**Note:** After import, run `terraform plan` to see if there are any differences between your running jobs and the .nomad.hcl files. Address any discrepancies before applying.

### 5. Apply Changes (Deploy)

```bash
terraform apply
```

Review the plan, type `yes` to confirm.

## Common Operations

### View Current State

```bash
terraform show
```

### List Managed Resources

```bash
terraform state list
```

### View Service Endpoints

```bash
terraform output service_endpoints
```

### Deploy Specific Services Only

Use `-target` to deploy specific resources:

```bash
terraform apply -target=nomad_job.minio -target=nomad_job.tusd
```

### Destroy Specific Service

```bash
terraform destroy -target=nomad_job.uppy
```

### Taint a Resource (Force Redeployment)

```bash
terraform taint nomad_job.grafana
terraform apply
```

### View Deployment Summary

```bash
terraform output deployment_summary
```

## Configuration

### Variable Overrides

Create `terraform.tfvars` for custom values:

```hcl
# terraform.tfvars
cluster_public_host = "my-cluster.example.com"
minio_root_password = "secure-password-here"
deploy_observability_stack = true
```

Or use command-line flags:

```bash
terraform plan -var="deploy_observability_stack=false"
```

### Deployment Toggles

Control which services deploy via variables:

```hcl
deploy_observability_stack = true   # Prometheus, Loki, Grafana, Alloy
deploy_optional_services   = false  # docker-registry, postgres, redis
deploy_boundary_worker     = true   # Boundary worker (system job)
```

### Experimental tier (opt-in, namespace `abc-experimental`)

Each experimental service has an `enable_<name>` variable, all default to `false`:

| Variable                | Service                                   |
| ----------------------- | ----------------------------------------- |
| `enable_postgres`       | shared postgres for experimental services |
| `enable_redis`          | shared redis                              |
| `enable_wave`           | Seqera Wave                               |
| `enable_supabase`       | Supabase stack                            |
| `enable_restic_server`  | restic backup server                      |
| `enable_caddy`          | Caddy gateway (alt to Traefik)            |
| `enable_xtdb`           | XTDB v2 bitemporal DB (jurist backend)    |

Apply with explicit opt-in:

```bash
terraform apply -var="enable_xtdb=true"
# or persist in terraform.tfvars:
echo 'enable_xtdb = true' >> terraform.tfvars
```

**Supabase-specific notes** (`nomad/experimental/supabase.nomad.hcl.tftpl`):

- Single Nomad job, one task group, **6 tasks sharing one bridge network
  namespace**: `db` (supabase/postgres), `db-init` (poststart sidecar that
  applies the deployment-specific SQL once), `meta` (postgres-meta), `auth`
  (GoTrue), `rest` (PostgREST), `studio`, `kong`. Inter-task traffic is on
  `127.0.0.1:<container-port>` (avoid `localhost` — resolves to IPv6 on
  this cluster and the postgres process binds IPv4 only).
- The image **must** be `supabase/postgres`, not vanilla `postgres:15-alpine`
  — supabase's fork ships pgsodium / pgjwt / pg_graphql / pg_net / pg_cron
  plus the auth/storage/realtime/_supabase migration scripts that the rest
  of the stack depends on.
- The image bakes its config at `/etc/postgresql/postgresql.conf` with
  `data_directory='/var/lib/postgresql/data'` hardcoded. We override that
  on the command line (`-c data_directory=/scratch/abc-supabase/pgdata`)
  so the bootstrap and the running server use the same path on the
  scratch host volume.
- `db-init` runs in the **same image** as `db` (so it has psql) and
  applies _supabase / realtime / webhooks / roles / jwt SQL once postgres
  is ready. The custom `roles.sql` resets `authenticator`, `pgbouncer`,
  `supabase_auth_admin`, etc. to var.supabase_postgres_password — without
  this, PostgREST will loop with `role "authenticator" does not exist`.
- Kong's declarative config (`kong.yml`) is rendered by the Nomad
  `template` block at deploy time with the consumer keys + dashboard
  creds substituted in directly — no envsubst pass at runtime.
- Kong service health check is `type = "tcp"` because the catch-all
  dashboard route returns 401 (basic-auth required) for every request,
  which Nomad's HTTP check would interpret as unhealthy.
- The default `supabase_postgres_password`, `supabase_jwt_secret`, anon
  key, service-role key, and `supabase_dashboard_password` are the
  well-known **insecure** values from supabase's own `.env.example`. They
  work out of the box for a demo but **must** be replaced before any
  data goes in. To generate proper secrets: pick a random ≥32 char
  `JWT_SECRET`, then sign two HS256 JWTs over it with `role=anon` and
  `role=service_role` (supabase publishes `utils/generate-keys.sh` for
  this).
- Endpoints (when enabled):
  - `http://<tailscale-ip>:8000/` — Studio (basic-auth: var.supabase_dashboard_username / _password)
  - `http://<tailscale-ip>:8000/rest/v1/`  — PostgREST API (apikey: var.supabase_anon_key)
  - `http://<tailscale-ip>:8000/auth/v1/`  — GoTrue Auth API
  - `http://<tailscale-ip>:8000/pg/`       — postgres-meta (apikey: var.supabase_service_role_key)
  - `http://supabase.aither/`             — Traefik vhost
- **Re-init: if the postgres bootstrap completes but the post-init
  scripts fail (e.g. you change `data_directory` after a half-broken
  first run), supabase roles like `authenticator` will not exist.**
  Wipe `/opt/nomad/scratch/abc-supabase/` on aither and redeploy.
  The simplest way to wipe: a one-shot `raw_exec` batch job pinned to
  aither running `rm -rf /opt/nomad/scratch/abc-supabase`.

**XTDB-specific notes** (`nomad/experimental/xtdb-v2.nomad.hcl.tftpl`):

- Pinned to `aither` (`var.xtdb_node`); persistent log + storage on the
  `scratch` host volume at `/opt/nomad/scratch/xtdb/{log,storage}`.
- Networking: `mode = "bridge"` with static port forwarding (host →
  container). Host mode does **not** work for containerd-driver containers
  on this cluster — they remain in their own netns and the bound port is
  unreachable on the host IP.
- Config delivery: a `raw_exec` prestart task (`write-config`) renders the
  YAML and copies it to `/opt/nomad/scratch/xtdb/config.yaml`. The XTDB task
  reads it via `args = ["-f", "/scratch/xtdb/config.yaml"]` (the scratch
  volume_mount makes that path resolve to the staged file). OCI bind mounts
  through the containerd-driver's `mounts` block do **not** apply reliably
  here — host volumes are the stable config-delivery path.
- Cold-start: the JVM takes ~4–5 min to reach `Node started`; the job's
  `update { healthy_deadline = "12m"; progress_deadline = "15m" }` and
  `check_restart { grace = "300s" }` accommodate this. The `nomad_job.xtdb`
  Terraform resource sets `detach = true` because the Nomad provider has a
  hardcoded 5-minute deployment-success timeout.
- Endpoints (when enabled):
  - `http://<tailscale-ip>:5555/healthz/ready` — Consul / external liveness probe
  - `psql -h <tailscale-ip> -p 15432 xtdb` — pgwire (any user/password works)
  - `http://xtdb.aither/healthz/ready` — Traefik vhost

## Dependency Graph

Services are deployed in this order:

```
1. traefik (reverse proxy)
   ├─→ 2. minio (storage)
   │   ├─→ 4. tusd (upload service)
   │   │   └─→ 5. uppy (upload UI)
   │   ├─→ 4. ntfy (notifications)
   │   │   └─→ 5. job_notifier
   │   └─→ 4. loki (logs)
   │       └─→ 5. grafana
   ├─→ 3. rustfs (alt storage)
   ├─→ 4. prometheus (metrics)
   │   └─→ 5. grafana
   │   └─→ 5. alloy (collector)
   └─→ 4. auth (authentication)
       └─→ 5. boundary_worker
```

View the full dependency graph:

```bash
terraform graph | dot -Tpng > graph.png
```

## Importing Existing Jobs

If services are already running, import them before managing with Terraform:

```bash
# General syntax
terraform import nomad_job.<resource_name> <nomad-job-id>

# Example
terraform import nomad_job.traefik abc-nodes-traefik
```

**Important:** The Nomad job ID must match the job name in the cluster, not the resource name in Terraform.

## State Management

### Local State (Current Setup)

State is stored in `terraform.tfstate` (local file). This is simple but:
- **Not safe for teams** - concurrent applies will conflict
- **No locking** - risk of corruption
- **Not backed up** - loss of file = loss of state

### Migrating to Remote State (Recommended for Production)

Migrate to Consul backend for team collaboration:

```hcl
# provider.tf
terraform {
  backend "consul" {
    address = "100.70.185.46:8500"
    path    = "terraform/abc-nodes"
    lock    = true
  }
}
```

Then migrate:

```bash
terraform init -migrate-state
```

## Troubleshooting

### Terraform wants to recreate jobs unnecessarily

This happens if the .nomad.hcl file differs from what's deployed. Options:

1. **Update the .nomad.hcl file** to match what's running
2. **Apply the change** if the Terraform version is correct
3. **Refresh state**: `terraform refresh`

### "Job not found" during import

Make sure:
- You're using the correct Nomad address (`NOMAD_ADDR`)
- The job name is exactly as shown in `nomad job status`
- You have the correct namespace (most jobs are in `default`)

### State lock errors

If using remote backend and Terraform crashes:

```bash
# Force unlock (use with caution)
terraform force-unlock <lock-id>
```

### Drift detection

Check if running jobs differ from Terraform state:

```bash
terraform plan -detailed-exitcode
```

Exit codes:
- `0` = no changes
- `1` = error
- `2` = changes detected

## Workflow Examples

### Adding a New Service

1. Create the `.nomad.hcl` file in `../nomad/`
2. Add a `resource "nomad_job"` block in `main.tf`
3. Add dependencies with `depends_on`
4. Add outputs in `outputs.tf` (optional)
5. Plan and apply:
   ```bash
   terraform plan
   terraform apply
   ```

### Updating Service Configuration

1. Edit the `.nomad.hcl` file
2. Preview changes:
   ```bash
   terraform plan
   ```
3. Apply:
   ```bash
   terraform apply
   ```

### Rolling Back a Change

```bash
# View state history
terraform show

# Revert the .nomad.hcl file via git
git checkout HEAD~1 nomad/minio.nomad.hcl

# Apply the old version
terraform apply
```

### Emergency Override

If you need to manually fix a job in production:

1. Make manual changes via Nomad CLI/UI
2. Update the `.nomad.hcl` file to match
3. Refresh Terraform state:
   ```bash
   terraform refresh
   ```
4. Verify no drift:
   ```bash
   terraform plan
   ```

## Integration with abc CLI

The Terraform setup complements the abc CLI:

```bash
# Deploy via Terraform
cd terraform
terraform apply

# Verify via abc CLI
abc job list

# Monitor logs
abc job logs abc-nodes-minio

# Check job status
abc admin services nomad cli -- job status abc-nodes-minio
```

## Best Practices

1. **Always run `terraform plan` before `apply`**
2. **Commit state changes** if using local backend (or use remote backend)
3. **Use variables** instead of hardcoding values
4. **Keep .nomad.hcl files as source of truth** - don't duplicate config in Terraform
5. **Import before managing** - don't recreate running services
6. **Use version control** - track all Terraform config changes in git
7. **Review dependency order** - ensure services start in correct sequence

## Comparison: Terraform vs Manual Deployment

| Aspect | Manual (`nomad job run`) | Terraform |
|--------|-------------------------|-----------|
| State tracking | ❌ Manual tracking | ✅ Automatic |
| Drift detection | ❌ None | ✅ Built-in |
| Dependency order | ⚠️ Manual scripts | ✅ Declarative |
| Rollback | ⚠️ Git + manual | ✅ State-based |
| Team collaboration | ❌ Conflicts | ✅ Locking |
| Preview changes | ❌ No | ✅ `terraform plan` |
| Documentation | ⚠️ README files | ✅ Self-documenting |

## Next Steps

1. **Import all running services** (see import commands above)
2. **Run `terraform plan`** and fix any discrepancies
3. **Apply initial state** with `terraform apply`
4. **Migrate to remote backend** (Consul) for team use
5. **Integrate into CI/CD** for automated deployments

## Resources

- [Terraform Nomad Provider Docs](https://registry.terraform.io/providers/hashicorp/nomad/latest/docs)
- [Terraform CLI Documentation](https://www.terraform.io/cli)
- [Nomad Job Specification](https://www.nomadproject.io/docs/job-specification)
- [abc-cluster-cli Documentation](../../README.md)
