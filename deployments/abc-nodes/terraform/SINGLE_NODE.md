# Single-node deployment (default since 2026-05-03)

The Terraform config in this directory now defaults to deploying every managed Nomad job onto a **single host** (`aither`) using the **`docker` task driver**. This document explains why, what changed, and how to revert to the previous multi-node containerd-driver setup if needed.

## Why single-node + docker

The simplest possible operational shape that still exercises the full seed-tended (abc-nodes enhanced) + abc-grove (jurist + xtdb + supabase + postgres + wave) stack:

- One node to debug, one set of host volumes, one place to read logs
- Docker driver because (a) most upstream nf-core / nextflow images are tested against Docker first, (b) for lab-mode experimentation any random `docker pull` works without a containerd-driver-specific check, (c) the `containerd-driver` mode-host networking trap has bitten this cluster before (see `nomad/README.md` "Operational notes")
- Avoids the cross-region scheduler complexity that the 7-node configuration imposes on a small operator team
- Reversible — change two variables, restart, and you have multi-node back

This shape is appropriate for: solo developer environments, presentation / demo deployments, the abc-seed onboarding "single shared server" extreme (per `$ABC_UNIVERSE/brainstorms/abc-seed-onboarding/`), and any CI / integration test that doesn't need a real fleet.

It is NOT appropriate for: production multi-tenant academic-group deployments (use multi-node containerd-driver mode), abc-grove → abc-cloud bridge testing (needs federation), GenPath Africa cross-jurisdiction validation (needs Nomad regions per jurisdiction).

## What changed

| Layer | Before | After |
|---|---|---|
| Default task driver (system services) | `containerd-driver` | `docker` |
| Job placement | Spread across 7 clients (aither, nomad00–04, OCI, GCP-spot) by Nomad scheduler | Pinned to `var.target_hostname` (default `aither`) via constraint |
| Hardcoded hostnames | Mix of `aither`, `nomad02`, none | All replaced with `var.target_hostname` |
| Terraform variables | (no single-node toggles) | `target_hostname`, `default_driver`, `single_node` added |
| `main.tf` `nomad_job` blocks | Some passed `hcl2.vars`, some didn't | All 27 blocks now pass `target_hostname` |

Every `.nomad.hcl(.tftpl)` in `nomad/`, `nomad/experimental/`, and `nomad/fx/` got:

1. A `variable "target_hostname"` block at the top.
2. Driver swap: `driver = "containerd-driver"` → `driver = "docker"` (51 swaps across the tree).
3. A constraint at the first task group level pinning to `var.target_hostname` (10 added; 37 already had a hostname constraint and were rewritten to use the variable).

## How to deploy

Identical to before; defaults are correct for the single-node setup:

```bash
cd deployments/abc-nodes/terraform
terraform plan
terraform apply
```

To override the target host (e.g. for a developer's own lab box):

```bash
terraform apply -var="target_hostname=mylabbox"
```

To disable hostname pinning entirely (let Nomad place freely — useful when re-spreading workloads after running multi-node):

```bash
# In each .nomad.hcl, the constraint will evaluate against an empty string.
# In Nomad, an empty value matches everything, effectively disabling the pin.
terraform apply -var="target_hostname="
```

(Verify your jobs accept this; some may still have hardcoded hostnames in places the conversion missed.)

## How to revert to multi-node containerd

Two options.

### Option A — selective override (no file edits)

Pass alternative drivers in tfvars or via -var:

```bash
terraform apply -var="target_hostname=" -var="default_driver=containerd-driver"
```

This works for `target_hostname` (effective immediately because every job uses `var.target_hostname` in the constraint). For driver, the variable is informational — you also need to edit each job spec's `driver = "docker"` back to `containerd-driver`. Use git to revert the driver swap:

```bash
git diff HEAD~1 -- deployments/abc-nodes/nomad/ | grep '^[+-]      driver'
git checkout HEAD~1 -- deployments/abc-nodes/nomad/*.nomad.hcl
git checkout HEAD~1 -- deployments/abc-nodes/nomad/experimental/*.nomad.hcl
```

(Adjust `HEAD~1` to whichever commit precedes the single-node conversion.)

### Option B — re-eligible the drained nodes via abc CLI / nomad CLI

This is the operational reversal — the Terraform-level change above is the architectural one. To get workloads spreading again, also re-eligible the drained client nodes:

```bash
# From a workstation with the management token + NOMAD_ADDR pointing at the leader:
for NODE in nomad00 nomad01 nomad02 nomad03 nomad04 oci-abhi-phd-arm-sa abc-dev-spot-lrzl.africa-south1-c.c.flow-168503.internal; do
  NODE_ID=$(nomad node status -json | jq -r ".[] | select(.Name==\"$NODE\") | .ID")
  nomad node drain -disable "$NODE_ID"
done
```

Then `terraform apply -var="target_hostname="` so newly-evaluated jobs spread across the now-eligible fleet.

## Caveats / what to know

- **`abc-nodes-alloy`** is a Nomad system job. It runs one alloc per *eligible* client by definition. The hostname constraint applied during the single-node conversion has no practical effect on system jobs; it would already be running only on the one eligible client (aither). Re-eligibling other nodes will spread alloy back across them, which is what you want.
- **Driver swap caveats**: the Docker driver supports Nomad's `volume_mount` and `network { mode = "bridge" }` stanzas (same as containerd-driver), so the migration is mostly transparent. But the Docker driver does NOT support some containerd-specific config keys (`stats_interval`, `containerd_runtime`). Those were on aither's client.hcl plugin block, not in the job specs, so they weren't touched by this conversion.
- **Aither must have docker engine running and healthy**. Already true (see `$ABC_UNIVERSE/specs/completed/aither-as-client-and-lab.md` acceptance criteria).
- **`allow_privileged = true`** must be set on the docker plugin in aither's `/etc/nomad.d/client.hcl` for any job that runs privileged containers. Already true (per the same spec).
- **Resource pressure**: on a single host with the full stack, aither sits at ~load 35 / 48 cores under typical load. Adding heavy experimental jobs (full Wave layer-build, large nf-core pipelines) can push it. Monitor with `uptime`, `nomad node status -self`.

## Naming / layering note

The ABC-cluster product line names (`abc-seed`, `abc-grove`, `abc-cloud`) are used in the broader vision (see `$ABC_UNIVERSE/vision/abc-cluster-vision.md`). This Terraform config is operationally what an `abc-seed` (with `seed-tended` floor) deployment looks like when constrained to one host — plus the abc-grove components (jurist, xtdb, postgres, supabase) opt-in via the `enable_*` flags.

## Related

- `$ABC_UNIVERSE/specs/completed/aither-as-client-and-lab.md` — the spec that converted aither to a pure Nomad client and added the docker driver to it
- `$ABC_UNIVERSE/design/reference/abc-nodes-deployment.md` — the holistic deployment reference that documents the operational tier model
- `$ABC_UNIVERSE/brainstorms/abc-seed-onboarding/` — three-extreme onboarding brainstorm that motivates the single-shared-server case this config now serves cleanly
- `nomad/README.md` (this repo) — operational notes including the "Operational notes" section that flagged the containerd `mode = "host"` bug
- `clients/aither-client.hcl` — the matching Nomad client agent config (notes the docker plugin block + caveats)
