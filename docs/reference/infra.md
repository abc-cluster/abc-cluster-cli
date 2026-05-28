---
sidebar_position: 8
---

# infra

Compute node and storage management. Requires `--sudo` or `--cloud` elevation.

:::warning Deferred capability — not part of the shipped seedling product surface

The **mutating** commands in this group (`abc infra compute add`, and any future
`promote`) are **not** the supported path for seedling-tier cluster setup
today. Seedling infrastructure provisioning is the responsibility of the
separate **`abc-deployments`** project (Pulumi-driven): `pulumi up` against
a stack configuration installs Nomad, MinIO, Tailscale, the observability
stack, and the seedling-tended hygiene configuration. The CLI's
seedling-tier role is **observational only** — `abc cluster status`,
`abc cluster capabilities show`, `abc cluster doctor`, `abc auth context
list`, `abc data ls`, `abc pipeline list`.

The scaffolding here is retained for a forward-looking capability: at the
`abc-cloud` tier, operators who provision their own VMs in their own cloud
account will use `abc infra compute add` to register those VMs as Nomad
workers against an abc-cloud-managed control plane, without operating
their own deployment codebase. **That cloud-tier capability is not shipped
today.** Operators following the seedling-tier deployment guide should
use `abc-deployments`, not these commands.

**Read-only** commands in this section — `infra compute list`, `infra compute
show`, `infra compute probe`, `infra compute node debug`, and `infra
storage size` — are operational at all tiers and are the supported
observability surface for inspecting an already-provisioned cluster.

:::

## infra compute add

> **Deferred** — see the warning above. The command may execute today, but
> it is not the supported seedling-tier workflow and is not exercised by
> the abc-seedling validation archetypes.

Register a new compute node with the cluster:

```bash
abc infra compute add \
  --host <ip-or-hostname> \
  --name <node-name> \
  [--driver containerd|docker] \
  [--datacenter dc1]
```

## infra compute list

```bash
abc infra compute list
```

## infra compute show

```bash
abc infra compute show <node-name>
```

## infra compute probe

Run connectivity and capability checks against a node:

```bash
abc infra compute probe <node-name>
```

## infra compute node debug

Open a debug shell on a node allocation:

```bash
abc infra compute node debug <alloc-id>
```

## infra storage size

Report used and available storage across the cluster:

```bash
abc infra storage size
```
