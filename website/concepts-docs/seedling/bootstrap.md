---
title: Phase 2 — Pulumi bootstrap
---

# Phase 2 — Pulumi bootstrap

With a bare Nomad node running (Phase 1), the Pulumi stack in the
`abc-deployments` repository installs all abc-cluster services on top:
MinIO, tusd (resumable uploads), the monitoring stack, and any supporting
Nomad jobs.

> **Status:** The `abc-seedling` Pulumi stack is in active development.
> The stack source and detailed usage instructions live in the
> `abc-deployments` repository. This page describes what the stack does
> and the inputs it needs from Phase 1.

## What the Pulumi stack installs

| Service | Nomad job | Purpose |
|---|---|---|
| MinIO | `abc-minio` | Object storage — one bucket per group |
| tusd | `abc-tusd` | Resumable browser uploads (`/files/` endpoint) |
| Grafana | `abc-grafana` | Dashboards for job and node metrics |
| Prometheus | `abc-prometheus` | Metrics collection |
| MinIO groups + policies | (provisioned via `mc`) | Per-group ACL bucket isolation |

The landing page and claim service (`abc-landing-api`) are deployed
separately in Phase 4 — they depend on the pool database being populated
first (Phase 3).

## Prerequisites

From Phase 1 you need:

- Nomad management token
- Nomad HTTP address (e.g. `http://your-node:4646`)
- `su-*` namespaces created

You also need:

- [Pulumi CLI](https://www.pulumi.com/docs/install/) installed on your deploy machine
- Node.js 18+ (for the TypeScript Pulumi program)
- SSH access to the cluster node (for `scp` and `ssh` within the stack)

## Run the stack

```bash
# Clone the abc-deployments repository
git clone https://github.com/abc-cluster/abc-deployments
cd abc-deployments/abc-seedling-stack   # path TBC — check the repo

# Install dependencies
npm install

# Configure the stack
pulumi stack init my-cluster
pulumi config set nomadAddr    http://your-node:4646
pulumi config set nomadToken   <management-token> --secret
pulumi config set clusterHost  your-cluster.example.com
pulumi config set groups       "lab1,lab2,demo"

# Deploy
pulumi up
```

The stack will:

1. Submit all Nomad jobs (MinIO, tusd, monitoring).
2. Wait for each job to reach `running` status before proceeding.
3. Run `mc` commands to create MinIO groups and attach bucket policies.
4. Print the MinIO root credentials and service addresses on completion.

## Verify services

After `pulumi up` completes:

```bash
# All abc jobs should be running
nomad job status

# MinIO is healthy
curl http://your-node:9000/minio/health/ready   # 200 OK

# tusd is accepting uploads
curl http://your-node:1080/files/               # 405 Method Not Allowed (correct)
```

If you configured a hostname and DNS is pointing at the node:

```
https://minio.your-cluster.example.com/    MinIO console
https://nomad.your-cluster.example.com/    Nomad UI
https://grafana.your-cluster.example.com/  Grafana dashboards
```

## What you have at the end of Phase 2

- Nomad running with ACLs and `su-*` namespaces.
- MinIO running with per-group bucket policies.
- tusd running for browser uploads.
- Monitoring stack running (optional but included by default).
- MinIO root credentials (needed for Phase 3 provisioning).

## Next step

→ [Phase 3: Provision the access pool](provision)
