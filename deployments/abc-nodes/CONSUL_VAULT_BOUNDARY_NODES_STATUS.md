# Consul / Vault / Boundary on Nomad Server Nodes — Status & Resumption Guide

**Last updated:** 2026-04-26  
**Status:** PAUSED — partial deployment, cleaned up to stable state; Tailscale split-DNS working

---

## Context

The abc-nodes Nomad cluster has four categories of nodes:

| Node | Datacenter | Role | Tailscale IP |
|---|---|---|---|
| aither | default | Nomad non-voter, runs all services (Consul server, Vault, Boundary controller) | 100.70.185.46 |
| nomad00 | sun-nomadlab | Nomad Raft Voter + Client | 100.108.199.30 |
| nomad01 | sun-nomadlab | Nomad Raft Leader + Client | 100.77.21.36 |
| oci-abhi-phd-arm-sa | oci-nomadlab | Nomad Raft Voter + Client | 100.68.49.95 |
| nomad02-04 | sun-nomadlab | Nomad Clients only | — |

The goal of this work was to deploy Consul clients + Vault binary on the **Nomad server nodes** (nomad00, nomad01, oci) so that:
1. Consul service discovery works cluster-wide (not just on aither)
2. Boundary workers can resolve `*.service.consul` DNS names
3. Boundary SSH targets can be registered for all cluster nodes

---

## Tailscale Split-DNS Setup (2026-04-26) ✅

### Problem
After the Tailscale Caddy job (`abc-experimental-caddy-tailscale`) was deployed to serve `*.aither` on `100.70.185.46`, clicking links in the landing page failed to resolve — even after configuring Tailscale split-DNS.

### Root cause
Two bugs in the original `deploy-consul.sh` dnsmasq config:

1. **Wrong listen address** — `/etc/dnsmasq.d/00-listen.conf` bound dnsmasq to `127.0.0.1` only (`listen-address=127.0.0.1`, `bind-interfaces`). Tailscale split-DNS clients send UDP/53 queries to `100.70.185.46`, which received no response (connection timeout).

2. **Wrong resolution target** — `/etc/dnsmasq.d/20-aither.conf` resolved `*.aither` to `146.232.174.77` (the LAN IP). The Tailscale Caddy job binds on `100.70.185.46`, not the LAN IP, so even if DNS had resolved, Caddy would not have answered on that IP.

### Fix applied (2026-04-26)
Applied via a Nomad raw_exec batch job (`fix-dnsmasq-listen`) since passwordless sudo is not available:

```bash
# Written to /etc/dnsmasq.d/00-listen.conf on aither:
listen-address=127.0.0.1,100.70.185.46
bind-interfaces

# /etc/dnsmasq.d/20-aither.conf was already correct:
address=/.aither/100.70.185.46
```

`systemctl restart dnsmasq` applied the change. Verified:
```bash
dig @100.70.185.46 nomad.aither +short   # → 100.70.185.46
curl --resolve "grafana.aither:80:100.70.185.46" http://grafana.aither/  # → 302
```

### Tailscale admin console config
DNS → Add nameserver → `100.70.185.46`, restrict to domain: **`aither`** (no leading dot).  
All tailnet devices now resolve `*.aither` via dnsmasq on aither and reach the Tailscale Caddy.

### Scripts updated
- `consul/deploy-consul.sh` — `00-listen.conf` now includes `${AITHER_TS_IP}`; `20-aither.conf` uses `${AITHER_TS_IP}`; smoke test verifies the Tailscale listener
- `consul/install-consul-via-nomad.nomad.hcl` — added `var.aither_ts_ip`; all three task groups use it for `20-aither.conf`
- `consul/deploy-consul-to-server-nodes.sh` — `20-aither.conf` uses TS IP (server nodes are on Tailscale)
- `caddy/Caddyfile.lan` — updated architecture comment

### Client-side DNS flush (if resolution seems stale)
```bash
# macOS
sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder

# Linux / systemd-resolved
resolvectl flush-caches

# Verify split-DNS is hitting the right nameserver
scutil --dns | grep -A5 "aither"     # macOS
resolvectl query nomad.aither         # Linux
```

---

## What Was Accomplished

### 1. Boundary Worker — Fixed and Running on All Nodes ✅

**File:** `deployments/abc-nodes/nomad/boundary-worker.nomad.hcl`

**Changes made:**
- Fixed `public_addr` — was `0.0.0.0:9203` (caused `boundary connect ssh` to hang); changed to use Tailscale IP via `{{ env "attr.unique.advertise.address" | regexReplaceAll ":.*$" "" }}:9203`
- Changed service `provider = "consul"` → `provider = "nomad"` — the consul provider auto-injects `${attr.consul.version} >= 1.8.0` which silently excluded all non-Consul nodes; switching to nomad provider removed that hidden constraint
- Changed `initial_upstreams` from `abc-nodes-boundary-cluster.service.consul:9201` (only resolvable on aither) to direct Tailscale IP `100.70.185.46:9201`
- Inlined the KMS worker-auth key directly (removed nomadVar dependency that was causing placement failures)

**Result:** 6+ Boundary workers now connect and show up in Boundary UI. `boundary connect ssh` no longer hangs.

**Current state:** Job is running as a system job across all cluster nodes. No further changes needed.

---

### 2. Consul Client Install on Server Nodes — Partial ⚠️

**Files created:**
- `deployments/abc-nodes/consul/install-consul-via-nomad.nomad.hcl` — Nomad batch job that installs Consul 1.19.2 via raw_exec (runs as root), for nomad00, nomad01, oci groups
- `deployments/abc-nodes/consul/deploy-consul-to-server-nodes.sh` — SSH-based deployment script using hashi-up (requires passwordless sudo for `abhinav` user — not available)
- `deployments/abc-nodes/consul/consul-client.hcl` — Consul client config template (uses `NODE_TAILSCALE_IP` placeholder)

**What ran:** The `install-consul-via-nomad.nomad.hcl` job was submitted and ran on nomad00 and nomad01.

**What succeeded:**
- Consul 1.19.2 binary installed at `/usr/local/bin/consul`
- `consul` user created
- `/etc/consul.d/consul.hcl` written (pointing at aither 100.70.185.46 as server)
- `/etc/systemd/system/consul.service` written
- Consul service enabled and started — **consul is now installed** on nomad00 and nomad01

**What failed / was left broken:**
- `dnsmasq` failed to start on both nodes (reason not fully diagnosed; likely a conflict with existing network config)
- The install script disabled `systemd-resolved`'s stub listener (`DNSStubListener=no`) before dnsmasq was confirmed working
- As a result: `/etc/resolv.conf` pointed to `127.0.0.53` (systemd-resolved stub) which was no longer listening → **external DNS broken on nomad00 and nomad01**

**Cleanup done (2026-04-26):**
- Ran `restore-dns-server-nodes` Nomad batch job on both nodes:
  - Removed `/etc/systemd/resolved.conf.d/no-stub.conf`
  - Restarted `systemd-resolved` (stub re-enabled on 127.0.0.53)
  - Stopped and disabled `dnsmasq`
  - Stopped and disabled `consul`
  - Renamed `/etc/nomad.d/consul.hcl` → `/etc/nomad.d/consul.hcl.disabled`
- **DNS is now restored** on nomad00 and nomad01

**Current state of nodes:**
- Consul binary: installed at `/usr/local/bin/consul`
- Consul config: `/etc/consul.d/consul.hcl` (present, not active)
- Consul service: installed but **disabled and stopped**
- dnsmasq: installed but **disabled and stopped**
- Nomad consul stanza: renamed to `.disabled`, not loaded
- External DNS: **working** (systemd-resolved restored)

**OCI node (oci-abhi-phd-arm-sa):** The oci task group in the install job did not have a hostname constraint, so it may have run on the wrong node. **Status on OCI is unknown — assume not deployed.**

---

### 3. Vault on Server Nodes — Not Deployed ❌

**File created:** `deployments/abc-nodes/vault/deploy-vault-to-server-nodes.sh`

Uses hashi-up to install Vault binary + write `/etc/nomad.d/vault.hcl` (pointing Nomad at aither's Vault: `http://100.70.185.46:8200`).

**Never ran** — requires SSH passwordless sudo for `abhinav` user on server nodes, which is not available. The same `raw_exec` approach as the consul job would work.

**Current state:** Not deployed on any node.

---

### 4. Boundary SSH Targets — Not Configured ❌

Waiting on Consul + Vault being stable on all nodes. Not started.

---

### 5. Unit Test Fix ✅

**File:** `cmd/utils/nomad_pack_cli_test.go`

Added `cluster_type: abc-nodes` to the test config YAML in `TestRunNomadPackCLI_InjectsNomadEnvFromActiveContext`. Without this, `IsABCNodesCluster()` returned false and `AbcNodesNomadNamespaceForCLI()` returned empty string instead of `"apps"`.

---

### 6. Integration Tests — Verified ✅

All unit tests pass. Integration tests:

| Test | Result | Notes |
|---|---|---|
| `TestIntegration_HCLParseRoundTrip` | PASS | |
| `TestIntegration_DryRunDoesNotRegisterJob` | PASS | |
| `TestIntegration_SubmitMinimalExecJob` | PASS (isolation) / FLAKY (suite) | Transient under cluster load |
| `TestIntegration_FailingJobReachesDeadStatus` | PASS | |
| `TestIntegration_SubmitWithNameOverride` | PASS | |
| `TestIntegration_SubmitMultiNode` | PASS | |
| `TestIntegration_WatchStreamsStdout` | PASS (isolation) / FLAKY (suite) | Transient under cluster load |
| `TestIntegration_ExecDriverCompletes` | PASS | |
| `TestIntegration_DockerDriverCompletes` | FAIL | Docker Hub unreachable from cluster nodes |
| `TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive` | SKIP | Requires `ABC_INTEGRATION_OBS_STACK=1` |
| `TestIntegration_StressNgCPUWorkloadCompletes` | SKIP | Requires `ABC_INTEGRATION_STRESS_NG=1` |

**Run integration tests against the Nomad leader (nomad01), not aither:**
```bash
cd analysis/packages/abc-cluster-cli
NOMAD_ADDR="http://100.77.21.36:4646" \
NOMAD_TOKEN="$(grep nomad_token ~/.config/abc/config.yaml | head -1 | awk '{print $2}')" \
ABC_TEST_TIMEOUT=300 \
go test -tags integration -count=1 -timeout=15m ./cmd/job/...
```

Note: Use `http://100.77.21.36:4646` (nomad01, the Raft leader), **not** `http://100.70.185.46:4646` (aither). Aither's Nomad returns 403 on most authenticated endpoints despite holding a valid management token.

---

## Resuming This Direction

When ready to add Consul + Vault on server nodes, here is the step-by-step plan:

### Step 1 — Fix dnsmasq on server nodes

Before re-deploying consul, diagnose why dnsmasq fails on nomad00/01. The most likely cause is a port conflict on 127.0.0.1:53 or a systemd-resolved ordering issue. A safe approach:

```bash
# Run a diagnostic job first:
# Check: dnsmasq --test && systemctl status dnsmasq --no-pager
# Then review the journalctl output for failure reason
```

Alternative: skip dnsmasq and use `systemd-resolved` to forward `.consul` queries:

```ini
# /etc/systemd/resolved.conf.d/consul.conf
[Resolve]
DNS=127.0.0.1:8600
Domains=~consul
```

This avoids dnsmasq entirely and is supported in systemd-resolved >= 239.

### Step 2 — Re-enable Consul on nomad00 and nomad01

The binary and config are already installed. After DNS strategy is decided:

```bash
# Via Nomad raw_exec batch job:
systemctl enable consul
systemctl start consul
# Verify: consul members
```

Update `/etc/nomad.d/consul.hcl.disabled` → `/etc/nomad.d/consul.hcl` and reload Nomad.

### Step 3 — Deploy Consul on OCI node

The OCI task group in `install-consul-via-nomad.nomad.hcl` needs a hostname constraint. Add:
```hcl
constraint {
  attribute = "${attr.unique.hostname}"
  value     = "oci-abhi-phd-arm-sa"
}
```
OCI uses Ubuntu; the same script should work. OCI Tailscale IP: `100.68.49.95`.

### Step 4 — Deploy Vault binary on server nodes

Create a Nomad raw_exec batch job (same pattern as consul install) that:
1. Downloads Vault 1.18.4 binary
2. Writes `VAULT_ADDR=http://100.70.185.46:8200` to `/etc/environment`
3. Writes `/etc/nomad.d/vault.hcl`:
   ```hcl
   vault {
     enabled = true
     address = "http://100.70.185.46:8200"
   }
   ```
4. Reloads Nomad

Reference: `deployments/abc-nodes/vault/deploy-vault-to-server-nodes.sh` (the SSH version).

### Step 5 — Update boundary-worker to use Consul DNS

Once Consul is running on all nodes, revert these two lines in `boundary-worker.nomad.hcl`:
```hcl
# Change back to Consul DNS:
initial_upstreams = ["abc-nodes-boundary-cluster.service.consul:9201"]

# Change service provider back to consul:
service {
  provider = "consul"
  ...
}
```

### Step 6 — Register nodes as Boundary SSH targets

After Consul + Vault are stable, add each cluster node as a Boundary SSH target:
1. In Boundary UI or via CLI, create a host in the `abc-nodes` host catalog for each node
2. Add to the `abc-nodes-ssh` host-set
3. Configure credential injection (Vault SSH cert signer or static credential store)
4. Test: `boundary connect ssh -target-id <id>`

---

## Key IP Addresses

| Service | Address |
|---|---|
| Nomad Leader (nomad01) | http://100.77.21.36:4646 |
| Nomad API (aither, non-voter) | http://100.70.185.46:4646 (has ACL issues) |
| Consul server (aither) | 100.70.185.46:8500 |
| Vault server (aither) | http://100.70.185.46:8200 |
| Boundary controller (aither) | 100.70.185.46:9200 (API), 100.70.185.46:9201 (cluster) |

---

## Files Created in This Work

```
deployments/abc-nodes/
├── consul/
│   ├── consul-client.hcl                      # Consul client config template (NODE_TAILSCALE_IP placeholder)
│   ├── deploy-consul-to-server-nodes.sh       # SSH/hashi-up deploy script (requires passwordless sudo)
│   └── install-consul-via-nomad.nomad.hcl     # Nomad batch job approach (works, ran on nomad00/01)
├── vault/
│   └── deploy-vault-to-server-nodes.sh        # SSH/hashi-up deploy script (never ran, same sudo issue)
├── nomad/
│   └── boundary-worker.nomad.hcl              # MODIFIED — provider=nomad, direct IP, public_addr fix
└── dns/
    └── flush-dns-cache.sh                     # Client-side DNS flush + split-DNS verification

cmd/utils/
└── nomad_pack_cli_test.go                     # FIXED — added cluster_type: abc-nodes to test config
```
