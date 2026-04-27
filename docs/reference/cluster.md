---
sidebar_position: 10
---

# cluster

Sync and inspect cluster capability metadata.

## cluster capabilities sync

Pull the capability manifest from the active cluster into local config:

```bash
abc cluster capabilities sync
```

This updates which features, drivers, and service endpoints the cluster advertises. Run after provisioning new nodes or enabling new services.

## cluster capabilities show

Display the cached capabilities for the active cluster:

```bash
abc cluster capabilities show
```

Outputs a structured table of: available datacenters, node drivers (containerd, docker), installed service versions, and feature flags.

## accounting / emissions / compliance

High-level cluster reporting commands (require `--cloud` elevation):

```bash
abc accounting                 # usage and cost report
abc emissions                  # carbon/energy estimates
abc compliance                 # compliance status summary
```
