# Namespace Mismatch Issue

## Problem Summary

There is a **critical mismatch** between namespace declarations in `.nomad.hcl` files and the actual deployment:

| Service | Declared Namespace (in .nomad.hcl) | Actual Namespace (in Nomad) |
|---------|-----------------------------------|----------------------------|
| traefik | `abc-services` | `default` ✅ |
| minio | `abc-services` | `default` ✅ |
| rustfs | `services` | `default` ✅ |
| prometheus | `abc-services` | `default` ✅ |
| loki | `abc-services` | `default` ✅ |
| grafana | `abc-services` | `default` ✅ |
| alloy | `abc-services` | `default` ✅ |
| tusd | `default` | `default` ✅ |
| uppy | `abc-applications` | `default` ✅ |
| ntfy | `abc-services` | `default` ✅ |
| job-notifier | `services` | `default` ✅ |
| abc-nodes-auth | `abc-services` | `default` ✅ |
| boundary-worker | `default` | `default` ✅ |
| docker-registry | `services` | `default` ✅ |

**Reality:** ALL 14 services are running in the `default` namespace.

## Impact on Terraform

When Terraform:
1. Reads `.nomad.hcl` files → sees `namespace = "abc-services"` (or others)
2. Tries to import jobs → looks in `default` namespace where they actually are
3. Compares state → detects mismatch and wants to recreate jobs

This will cause:
- ❌ Import failures
- ❌ Unnecessary job recreation attempts
- ❌ Potential service downtime
- ❌ State drift

## Root Cause

The `.nomad.hcl` files were likely:
1. Initially designed for multiple namespaces (`abc-services`, `services`, etc.)
2. Deployed manually to `default` namespace (override or different context)
3. Never updated to match actual deployment

## Solution: Fix the HCL Files

Run the provided script to correct all namespace declarations:

```bash
cd terraform
./fix-namespaces.sh
```

This will:
1. Scan all job files
2. Replace incorrect namespace declarations with `namespace = "default"`
3. Create `.bak` backups
4. Report changes

### What Gets Changed

**Before:**
```hcl
job "abc-nodes-traefik" {
  namespace   = "abc-services"  # ❌ WRONG
  region      = "global"
  # ...
}
```

**After:**
```hcl
job "abc-nodes-traefik" {
  namespace   = "default"  # ✅ CORRECT
  region      = "global"
  # ...
}
```

## Verification

After fixing, verify the changes:

```bash
# Check what was changed
git diff ../nomad/

# Verify no more namespace mismatches
for file in ../nomad/*.nomad.hcl; do
  echo "$(basename $file): $(grep -m1 '^  namespace' $file | awk -F'"' '{print $2}')"
done
```

All should show `default`.

## Commit Changes

Once verified:

```bash
git add ../nomad/*.nomad.hcl
git commit -m "fix: correct namespace declarations to match actual deployment (default)"
git push
```

## Why This Matters for Terraform

Terraform's `nomad_job` resource:
- Parses the `.nomad.hcl` file to determine expected configuration
- Compares against actual state in Nomad
- **Namespace is part of the job's identity**

If the file says `abc-services` but Nomad shows `default`, Terraform thinks:
> "This is a different job! I need to destroy the one in `default` and create a new one in `abc-services`"

This is **dangerous** and would cause unnecessary downtime.

## Alternative: Leave Files As-Is?

**Not recommended.** You could theoretically:
1. Keep .nomad.hcl files unchanged
2. Use Terraform's `consul_keys` or similar to override namespace
3. Accept perpetual drift warnings

But this is:
- ❌ Confusing for future maintainers
- ❌ Error-prone
- ❌ Against infrastructure-as-code principles

**The correct approach:** Source files should match reality.

## After Fixing

Once namespace declarations are corrected:
1. Terraform imports will work correctly
2. `terraform plan` will show minimal/no changes
3. Future deployments will be predictable
4. Documentation matches reality

## Questions?

See the main [README.md](README.md) for full Terraform usage instructions.
