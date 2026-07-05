# Nomad node configs — per-host reference

Files in this directory are **reference copies** of the live Nomad agent
config for each named node in the abc-nodes fleet.

| File | Host | Tailscale IP | Notes |
|------|------|-------------|-------|
| `aither-client.hcl` | aither | `100.70.185.46` | Single-node **server+client** (`bootstrap_expect = 1`), `node_pool = "platform"` — hosts MinIO/tusd/signup-svc/observability + Nextflow head jobs. Drivers: containerd-driver, docker, raw_exec, exec2, podman, singularity, qemu (built-in). |

## How to use

These configs are checked in here so changes can be reviewed and the per-node
configuration is visible to anyone working in this repo. They are **not**
deployed automatically by a Nomad job or Terraform — placement is manual:

```bash
# Validate, then copy to the host and restart
scp deployments/abc-nodes/clients/aither-client.hcl aither:/tmp/server.hcl.new
ssh aither nomad config validate /tmp/server.hcl.new
ssh aither 'sudo cp -a /etc/nomad.d/server.hcl /etc/nomad.d/server.hcl.bak-$(date +%Y%m%d%H%M%S) && \
    sudo cp /tmp/server.hcl.new /etc/nomad.d/server.hcl && \
    sudo chown root:root /etc/nomad.d/server.hcl && sudo chmod 640 /etc/nomad.d/server.hcl && \
    sudo systemctl restart nomad'

# Verify
ssh aither 'nomad node status -verbose $(nomad node status | grep aither | awk "{print \$1}")'
```

Restarting `nomad` on this node is a well-verified low-risk operation
(2026-05-19, re-confirmed 2026-07-04) — it restarts the Nomad *agent*
process only, not the Docker containers it supervises, so every running
allocation on the node stays up across the restart.

## Important — this file has drifted from the live host before

**Twice.** Once at inception (this file originally described aither as a
pure Nomad *client* joining external servers over Tailscale MagicDNS — a
topology aither hasn't used in months) and once for roughly two months
after the live host moved to its current single-node server+client
architecture without anyone updating this copy (found 2026-07-04 while
investigating an unrelated issue — see
`brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md`
in abc-universe). **When in doubt about what's actually live on aither,
verify directly** (`nomad node status -verbose <node-id>` against the
cluster API, or SSH in and read `/etc/nomad.d/server.hcl`) rather than
trusting this file. There is no automated drift-detection between this
reference copy and the live host today.
