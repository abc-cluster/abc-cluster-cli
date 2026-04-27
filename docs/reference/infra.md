---
sidebar_position: 8
---

# infra

Compute node and storage management. Requires `--sudo` or `--cloud` elevation.

## infra compute add

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
