# Nomad client configs — per-host reference

Files in this directory are **reference copies** of the Nomad client config
for each named client node in the abc-nodes fleet.

| File | Host | Tailscale IP | Notes |
|------|------|-------------|-------|
| `aither-client.hcl` | aither | `100.70.185.46` | Lab node — containerd-driver + docker + raw_exec + exec + java |

## How to use

These configs are checked in here so changes can be reviewed and the per-node
configuration is visible to anyone working in this repo. They are **not**
deployed automatically by a Nomad job or Terraform — placement is manual:

```bash
# Copy to the host and restart
scp deployments/abc-nodes/clients/aither-client.hcl aither:/etc/nomad.d/client.hcl
ssh aither sudo systemctl restart nomad

# Verify the node registered against the leader (not aither's own :4646)
nomad node status -address=http://100.77.21.36:4646
```

After restarting, watch `journalctl -u nomad -f` on aither until you see
`Node registered` and all five drivers (`containerd-driver`, `docker`,
`raw_exec`, `exec`, `java`) reported `Healthy` before proceeding with any
workload deploys.

## Important — tailnet suffix

The `servers` list in each `.hcl` uses `<tailnet>` as a placeholder for
the actual Tailscale tailnet name. Substitute it before deploying.
See the comment at the top of each file for details.
