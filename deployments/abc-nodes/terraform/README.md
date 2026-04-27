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

## Namespaces

The Terraform config provisions **four** Nomad namespaces and places each
managed job into one of them. They are created automatically by
`terraform apply` (they're standalone `nomad_namespace` resources), so the
operator does not have to pre-create them.

| Namespace          | Owner of                                                     |
| ------------------ | ------------------------------------------------------------ |
| `abc-services`     | Enhanced platform services (traefik, rustfs, garage, observability, ntfy, docs, …) |
| `abc-applications` | Enhanced user-facing apps                                    |
| `abc-experimental` | WIP / opt-in services (postgres, supabase, xtdb, caddy_tailscale, …) |
| `abc-automations`  | fx hooks (fx_notify, fx_tusd_hook, fx_archive)               |

> **Historical note:** an earlier iteration of this README warned that all
> jobs were colliding in the `default` namespace and pointed at a
> `fix-namespaces.sh` script. That problem is fixed: every Terraform-managed
> jobspec declares its target namespace, and the four namespace resources
> in `main.tf` are listed as the `depends_on` head of every job. If you see
> jobs in `default`, they were registered manually with `nomad job run` and
> should be migrated.

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

Only Terraform-managed jobs need importing — `minio`, `tusd`, `uppy`, and
`auth` are basic-tier (owned by the abc CLI) and are NOT in this Terraform
config, so do not try to import them here.

The job IDs all carry the `@<namespace>` suffix so the import targets the
right namespace:

```bash
# Namespaces (created by Terraform — only import if you pre-created them)
terraform import nomad_namespace.abc_services     abc-services
terraform import nomad_namespace.abc_applications abc-applications
terraform import nomad_namespace.abc_experimental abc-experimental
terraform import nomad_namespace.abc_automations  abc-automations

# Enhanced tier — networking + storage
terraform import nomad_job.traefik             "abc-nodes-traefik@abc-services"
terraform import nomad_job.rustfs              "abc-nodes-rustfs@abc-services"
terraform import nomad_job.garage              "abc-nodes-garage@abc-services"
terraform import nomad_job.docs                "abc-nodes-docs@abc-services"

# Enhanced tier — observability (count-conditional)
terraform import 'nomad_job.prometheus[0]'     "abc-nodes-prometheus@abc-services"
terraform import 'nomad_job.loki[0]'           "abc-nodes-loki@abc-services"
terraform import 'nomad_job.grafana[0]'        "abc-nodes-grafana@abc-services"
terraform import 'nomad_job.alloy[0]'          "abc-nodes-alloy@abc-services"

# Enhanced tier — notifications, system, optional
terraform import nomad_job.ntfy                "abc-nodes-ntfy@abc-services"
terraform import nomad_job.job_notifier        "abc-nodes-job-notifier@abc-services"
terraform import 'nomad_job.boundary_worker[0]' "abc-nodes-boundary-worker@abc-services"
terraform import nomad_job.abc_backups         "abc-backups@abc-services"

# Experimental tier (only if enabled)
terraform import nomad_job.xtdb                "abc-experimental-xtdb@abc-experimental"
terraform import nomad_job.postgres            "abc-experimental-postgres@abc-experimental"
terraform import nomad_job.supabase            "abc-experimental-supabase@abc-experimental"
terraform import nomad_job.caddy_tailscale     "abc-experimental-caddy-tailscale@abc-experimental"

# Automations tier
terraform import nomad_job.fx_notify    "fx-notify@abc-automations"
terraform import nomad_job.fx_tusd_hook "fx-tusd-hook@abc-automations"
terraform import nomad_job.fx_archive   "fx-archive@abc-automations"

# (Truncated reference — for the full set, see main.tf "IMPORT HINTS" header.)

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
# Direct host:port endpoints for the enhanced tier
terraform output service_endpoints

# Production-shape *.aither vhosts (resolved by caddy_tailscale on host :80)
terraform output public_endpoints

# Direct host:port endpoints for opt-in experimental services
terraform output experimental_endpoints

# Garage / restic secrets (sensitive — fetch with -raw and stash in 1Password)
terraform output -raw garage_admin_token
terraform output -raw restic_repo_password
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

| Variable                | Service                                            |
| ----------------------- | -------------------------------------------------- |
| `enable_postgres`       | standalone vanilla postgres (separate from supabase's bundled `supabase/postgres` — use this for Wave or other workloads needing a generic relational DB) |
| `enable_redis`          | shared redis                                       |
| `enable_wave`           | Seqera Wave (needs postgres + redis)               |
| `enable_supabase`       | Supabase stack — full multi-task port of the upstream docker-compose (db + meta + auth + rest + studio + kong, all in one Nomad group) |
| `enable_xtdb`           | XTDB v2 bitemporal DB (backs the abc-jurist policy service) |
| `enable_docker_registry` | Local OCI registry (registry:2) — push laptop-built images, pull them in Nomad jobs. Persistent on aither's scratch volume. See "Local registry workflow" below |
| `enable_restic_server`  | **DEPRECATED** — superseded by `enable_abc_backups` (restic-on-Garage) in the enhanced tier |
| `enable_caddy`          | **DEPRECATED** — superseded by `enable_caddy_tailscale` (the unified gateway in the enhanced tier). The two cannot run together; both want host port 80 |

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

### Local registry workflow (`enable_docker_registry`)

The `registry:2` job in the experimental tier lets you push images you've
built locally and reference them in any other Nomad job by their
`<tailscale-ip>:5000/<name>:<tag>` coordinates.

> **Full how-to** with troubleshooting, multi-arch notes, and a clean-up
> recipe lives at **`../docs/local-docker-registry.md`**. The summary below
> is enough to get going.

**One-time setup**

1. **On your laptop** — tell the docker daemon the registry is plain HTTP:

   ```jsonc
   // /etc/docker/daemon.json   (Linux) or Docker Desktop → Engine settings (mac/win)
   {
     "insecure-registries": ["100.70.185.46:5000"]
   }
   ```
   …then restart the daemon.

2. **On aither** — tell containerd the registry is plain HTTP, otherwise
   any Nomad job that references `100.70.185.46:5000/<image>` will fail
   to pull. The simplest way is the helper script:

   ```bash
   # From the abc-cluster-cli repo root:
   ./deployments/abc-nodes/scripts/configure-aither-registry-trust.sh
   #   --dry-run    print but don't execute
   #   --revert     remove the trust config
   ```

   Or do it manually:

   ```toml
   # /etc/containerd/certs.d/100.70.185.46:5000/hosts.toml
   server = "http://100.70.185.46:5000"

   [host."http://100.70.185.46:5000"]
     capabilities = ["pull", "resolve"]
     skip_verify = true
   ```
   …then `sudo systemctl restart containerd`. Confirm:
   `nerdctl --namespace nomad pull 100.70.185.46:5000/<your-image>:<tag>`

**Push from laptop**

```bash
docker tag my-app:dev 100.70.185.46:5000/my-app:dev
docker push 100.70.185.46:5000/my-app:dev
curl http://100.70.185.46:5000/v2/_catalog
# → {"repositories":["my-app"]}
```

**Reference in a Nomad jobspec**

```hcl
task "myapp" {
  driver = "containerd-driver"
  config {
    image = "100.70.185.46:5000/my-app:dev"
  }
}
```

**Inspect / delete tags**

```bash
# List tags for an image
curl http://100.70.185.46:5000/v2/my-app/tags/list

# Get the manifest digest (needed to delete)
curl -sI -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  http://100.70.185.46:5000/v2/my-app/manifests/dev | grep -i docker-content-digest

# Delete by digest (REGISTRY_STORAGE_DELETE_ENABLED is on)
curl -X DELETE http://100.70.185.46:5000/v2/my-app/manifests/sha256:<digest>

# Run garbage-collect on the registry container itself to reclaim disk:
nomad alloc exec -task registry -namespace abc-experimental \
  $(nomad job status -short abc-experimental-docker-registry | awk '/registry/ && /running/ {print $1; exit}') \
  registry garbage-collect /etc/docker/registry/config.yml
```

**Storage**

Images live at `/opt/nomad/scratch/docker-registry/` on aither (via the
`scratch` host volume). Wipe them all with:

```bash
abc admin services nomad cli -- job stop -purge abc-experimental-docker-registry
ssh sun-aither sudo rm -rf /opt/nomad/scratch/docker-registry
abc admin services cli terraform -- apply -auto-approve -target='nomad_job.docker_registry[0]' -var=enable_docker_registry=true
```

## Dependency Graph

Order in which `terraform apply` brings up the managed jobs (driven by
`depends_on` in `main.tf`). `minio`, `tusd`, `uppy`, and `auth` are
**basic-tier** (abc CLI managed, NOT in this graph — assume already
running before Terraform touches anything).

```
0. namespaces — abc-services / abc-applications / abc-experimental / abc-automations

1. traefik              (reverse proxy on host :8081 + dashboard :8888)
   │
   ├── 2. rustfs        (S3 storage, host :9900/:9901, "scratch" host volume)
   │   ├── 3. garage    (long-term archive S3, host :3900 + admin :3903)
   │   │   ├── 4. abc_backups   (periodic restic snapshots → garage; batch)
   │   │   └── 4. fx_archive    (RustFS → Garage tier-down; abc-automations)
   │   └── 3. docs      (Docusaurus static site → http://docs.aither)
   │
   ├── 2. prometheus → 3. loki → 4. grafana, 4. alloy   (observability)
   │
   ├── 2. ntfy → 3. job_notifier  (notifications)
   │           └── 3. fx_notify, 3. fx_tusd_hook  (abc-automations)
   │
   ├── 2. boundary_worker  (system job, all nodes)
   │
   ├── 2. caddy_tailscale  (unified gateway: owns host :80 across LAN +
   │                        Tailscale, proxies *.aither vhosts to traefik)
   │
   └── 2. (optional) docker_registry  (host :5000)

— Experimental tier (opt-in, `abc-experimental` namespace) —
   postgres ──┬─ wave
              └─ supabase  (own bundled postgres + 5 sidecar services)
   xtdb         (independent; pinned to aither)
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
