# Nomad node configs — per-host reference

Files in this directory are **reference copies** of the live Nomad agent
config for each named node in the abc-nodes fleet.

| File | Host | Tailscale IP | Notes |
|------|------|-------------|-------|
| `aither-client.hcl` | aither | `100.70.185.46` | Single-node **server+client** (`bootstrap_expect = 1`), `node_pool = "platform"` — hosts MinIO/tusd/signup-svc/observability + Nextflow head jobs. Drivers: containerd-driver, docker, raw_exec, exec2, podman, singularity, qemu (built-in). |
| `oci-af-client.hcl` | oci-af — **replacement box, 2026-07-06** | `100.89.64.44` | Pure Nomad **client** (no server stanza), `node_pool = "compute"`. Commercial OCI VM, OCI af-jhb (Johannesburg), Ubuntu 24.04. **This is the second oci-af** — the original (af-ubuntu2404, 32 vCPU/31 GiB, joined 2026-07-05) was decommissioned after an operator error (`cd` into a root-owned path silently failed, so a subsequent `sudo find … -exec rm -rf` ran against `/home/ubuntu` instead and likely deleted `~/.ssh`) broke SSH access irrecoverably. The replacement has different specs (8 vCPU/62 GiB), a new OS hostname (`oci-af-replacement-20260706194142` — differs from the Nomad `name` "oci-af", which is intentional/fine now that abc-cluster-cli's `--node`/`--pin-workers` attribute mismatch is fixed, PR #40/v0.1.69), and the SAME Tailscale identity/IP as before. Drivers: docker, raw_exec only. Host-volume tree (`abc-nomad/{scratch,nf-work,abc-tools}`) was deliberately laid out as a **sibling** of `DOCKER_ROOT/` on the mbovis disk this time — not nested inside or an ancestor of it — to avoid the mount-propagation bug the original box hit. Docker's data-root was already on the mbovis disk and the docker0→host-Tailscale-IP iptables fix was already present at first boot (both apparently baked into the provisioning image/snapshot used for this replacement) — see the file's own header comment for detail on both. `nf-work`/`abc-tools` manually populated with the cluster-pinned `s5cmd` (no cluster-wide sync mechanism exists — see `brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md` in abc-universe). Verified live end-to-end on this replacement: `nextflow-io/hello`, `nextflow-io/rnaseq-nf` (`-profile docker`), and `nf-core/demo` (`-profile test,docker`) all completed successfully pinned to this node via `--node oci-af --pin-workers`. |

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
