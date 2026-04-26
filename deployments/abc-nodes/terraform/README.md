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
