---
title: Phase 1 — Bare Nomad
---

# Phase 1 — Bare Nomad setup

The only hard prerequisite for a seedling deployment is a running Nomad node
with ACLs enabled. Everything else (MinIO, tusd, the claim service) is
installed on top by the Pulumi stack in Phase 2.

This page covers a minimal single-node Nomad install. If you already have
Nomad running with ACLs enabled and a management token, skip to
[Phase 2](bootstrap).

## Install Nomad

The official HashiCorp package repository is the recommended install path.
These steps work on Ubuntu 22.04 / Debian 12; adapt for your distribution.

```bash
# Add HashiCorp apt repository
wget -O - https://apt.releases.hashicorp.com/gpg | \
  sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg

echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] \
  https://apt.releases.hashicorp.com $(lsb_release -cs) main" | \
  sudo tee /etc/apt/sources.list.d/hashicorp.list

sudo apt-get update && sudo apt-get install -y nomad
```

Verify:

```bash
nomad --version
# Nomad v1.x.y
```

## Minimal configuration

Create `/etc/nomad.d/nomad.hcl`:

```hcl
# Minimal single-node Nomad configuration for abc-seedling.
# Runs as both server and client on the same node.

datacenter = "dc1"
data_dir   = "/opt/nomad/data"
log_level  = "INFO"

server {
  enabled          = true
  bootstrap_expect = 1
}

client {
  enabled = true
}

# ACLs — required for abc-cluster token model.
acl {
  enabled = true
}

# Ports — adjust if these conflict with existing services.
ports {
  http = 4646
  rpc  = 4647
  serf = 4648
}
```

Create the data directory and start the service:

```bash
sudo mkdir -p /opt/nomad/data
sudo systemctl enable --now nomad
```

## Bootstrap ACLs

ACLs start in a bootstrapped state — run this exactly once:

```bash
nomad acl bootstrap
# SecretID = <bootstrap-management-token>
```

Save the `SecretID`. This is your management token — treat it like a root
password. Store it securely (e.g. in a password manager or a secrets file
that is never committed to git).

Verify the cluster is up and the token works:

```bash
export NOMAD_ADDR=http://localhost:4646
export NOMAD_TOKEN=<bootstrap-management-token>

nomad node status
# ID        DC   Name    Class   Drain  Eligibility  Status
# df319cc0  dc1  aither  <none>  false  eligible     ready
```

You should see your node listed with `Status = ready`.

## Create namespaces

abc-cluster uses one Nomad namespace per group. The `default` namespace is
reserved for admin/infrastructure jobs.

Create namespaces for each group you plan to provision:

```bash
# Create a namespace for each research group
nomad namespace apply -description "Host genetics lab" su-hostgen
nomad namespace apply -description "Demo group" su-demo

# Verify
nomad namespace list
```

## Create the ACL policy for pool tokens

Pool tokens need write access to their own namespace. Create a policy file
and register it:

```bash
cat > /tmp/pool-policy.hcl << 'HCL'
# abc-seedling pool token policy.
# Applied per namespace — one policy covers all su-* namespaces.
namespace "su-*" {
  policy = "write"
  capabilities = [
    "submit-job", "dispatch-job", "read-job", "list-jobs",
    "read-logs", "read-fs", "alloc-exec", "alloc-node-exec",
    "list-jobs", "parse-job",
  ]
}
HCL

nomad acl policy apply -description "Pool token policy for su-* namespaces" pool-token /tmp/pool-policy.hcl
```

> **Note:** `provision.py` creates tokens with explicit namespace capability
> objects rather than using a named policy, so this policy registration is
> optional. It is useful if you want to create pool tokens manually via the
> Nomad UI or CLI.

## Verify

At the end of Phase 1 you should be able to run:

```bash
nomad status          # empty — no jobs yet, that's expected
nomad namespace list  # shows default + your su-* namespaces
nomad node status     # shows your node as ready
```

Keep your `NOMAD_ADDR` and management `NOMAD_TOKEN` — you'll need them for
Phase 2 (Pulumi bootstrap) and Phase 3 (pool provisioning).

## Next step

→ [Phase 2: Pulumi bootstrap](bootstrap)
