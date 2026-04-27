#!/usr/bin/env bash
# deploy-consul-to-server-nodes.sh
#
# Install Consul client agents on the Nomad SERVER nodes so they can:
#   1. Register services in the Consul catalog (Nomad → Consul integration)
#   2. Resolve *.consul DNS names (for Boundary worker initial_upstreams)
#
# Nomad server nodes (Raft voters — NOT just clients):
#   nomad00   abhinav@100.108.199.30  (sun-nomadlab, Voter)
#   nomad01   abhinav@100.77.21.36    (sun-nomadlab, Leader)
#   oci       ubuntu@129.151.174.199  (oci-nomadlab, Voter, Tailscale: 100.68.49.95)
#
# Consul server: aither at 100.70.185.46 (dc1, bootstrap_expect=1)
#
# Usage
# ─────
#   cd <repo-root>
#   bash deployments/abc-nodes/consul/deploy-consul-to-server-nodes.sh
#
# Prerequisites
# ─────────────
#   hashi-up in PATH (or accessible via abc admin services hashi-up cli)
#   SSH access to the nodes via ~/.ssh/id_ed25519

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSUL_VERSION="${CONSUL_VERSION:-1.19.2}"
SSH_KEY="${SSH_KEY:-${HOME}/.ssh/id_ed25519}"

# Consul server (aither) Tailscale IP
CONSUL_SERVER_TS_IP="100.70.185.46"

# abc / hashi-up runner — use abc wrapper when available
ABC_BIN="${ABC_BIN:-abc}"
HASHIUP_BIN="${HASHIUP_BIN:-hashi-up}"

# Function: run hashi-up via abc wrapper or directly
run_hashiup() {
  if command -v "${ABC_BIN}" &>/dev/null; then
    "${ABC_BIN}" admin services hashi-up cli -- "$@"
  else
    "${HASHIUP_BIN}" "$@"
  fi
}

# ── Node list ─────────────────────────────────────────────────────────────────
# Format: "SSH_HOST SSH_USER TS_IP DATACENTER"
declare -a NODES=(
  "100.108.199.30 abhinav 100.108.199.30 sun-nomadlab"
  "100.77.21.36   abhinav 100.77.21.36   sun-nomadlab"
  "129.151.174.199 ubuntu  100.68.49.95   oci-nomadlab"
)

# ── Deploy to each node ───────────────────────────────────────────────────────
for NODE_DEF in "${NODES[@]}"; do
  SSH_ADDR=$(echo "${NODE_DEF}" | awk '{print $1}')
  SSH_USER=$(echo "${NODE_DEF}" | awk '{print $2}')
  TS_IP=$(echo "${NODE_DEF}" | awk '{print $3}')
  DC=$(echo "${NODE_DEF}" | awk '{print $4}')

  echo ""
  echo "══════════════════════════════════════════════════════"
  echo "  Installing Consul on ${SSH_USER}@${SSH_ADDR} (TS: ${TS_IP})"
  echo "══════════════════════════════════════════════════════"

  # ── Patch consul-client.hcl with node's Tailscale IP ──────────────────────
  PATCHED_HCL=$(mktemp /tmp/consul-client-XXXX.hcl)
  sed "s/NODE_TAILSCALE_IP/${TS_IP}/g" "${SCRIPT_DIR}/consul-client.hcl" > "${PATCHED_HCL}"

  echo "==> Installing Consul ${CONSUL_VERSION} via hashi-up..."
  run_hashiup consul install \
    --ssh-target-addr "${SSH_ADDR}:22" \
    --ssh-target-user "${SSH_USER}" \
    --ssh-target-key  "${SSH_KEY}" \
    --version "${CONSUL_VERSION}" \
    --config-file "${PATCHED_HCL}"

  rm -f "${PATCHED_HCL}"

  echo "==> Configuring dnsmasq + resolv.conf on ${SSH_ADDR}..."
  # Server nodes (nomad00/01/oci) reach services over Tailscale, not the LAN.
  # *.aither must resolve to the Tailscale IP (100.70.185.46) where Caddy listens.
  AITHER_TS_IP="${CONSUL_SERVER_TS_IP}"

  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "AITHER_TS_IP='${AITHER_TS_IP}' bash -s" << 'REMOTE'
set -euo pipefail
echo "  Installing dnsmasq..."
apt-get install -y -qq dnsmasq 2>/dev/null || true

cat > /etc/dnsmasq.d/10-consul.conf << 'DNS'
server=/consul/127.0.0.1#8600
no-negcache
DNS

cat > /etc/dnsmasq.d/20-aither.conf << DNS
# Resolve *.aither to aither's Tailscale IP — server nodes connect via Tailscale.
address=/.aither/${AITHER_TS_IP}
DNS

cat > /etc/dnsmasq.d/00-listen.conf << 'DNS'
# Server nodes are not split-DNS nameservers; listen on loopback only.
listen-address=127.0.0.1
bind-interfaces
DNS

UPSTREAM=$(systemd-resolve --status 2>/dev/null \
  | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' \
  || echo "8.8.8.8")
cat > /etc/dnsmasq.d/30-upstream.conf << DNS
server=${UPSTREAM}
DNS

# Stop systemd-resolved stub
mkdir -p /etc/systemd/resolved.conf.d
cat > /etc/systemd/resolved.conf.d/no-stub.conf << 'RCONF'
[Resolve]
DNSStubListener=no
RCONF
systemctl restart systemd-resolved 2>/dev/null || true

systemctl enable dnsmasq
systemctl restart dnsmasq
sleep 2
systemctl is-active dnsmasq || { echo "ERROR: dnsmasq failed"; journalctl -u dnsmasq -n 20 --no-pager; exit 1; }

# Static resolv.conf → dnsmasq
chattr -i /etc/resolv.conf 2>/dev/null || true
rm -f /etc/resolv.conf
cat > /etc/resolv.conf << 'RESOLV'
nameserver 127.0.0.1
RESOLV
chattr +i /etc/resolv.conf

echo "  dnsmasq configured"
REMOTE

  echo "==> Adding Consul stanza to Nomad config on ${SSH_ADDR}..."
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "bash -s" << 'NOMAD_CONSUL'
NOMAD_CONSUL_DROP="/etc/nomad.d/consul.hcl"
if [[ ! -f "${NOMAD_CONSUL_DROP}" ]]; then
  cat > "${NOMAD_CONSUL_DROP}" << 'NC'
# Consul integration — written by deploy-consul-to-server-nodes.sh
consul {
  address = "127.0.0.1:8500"
}
NC
  echo "  Written Nomad consul stanza to ${NOMAD_CONSUL_DROP}"
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
  sleep 3
else
  echo "  Nomad consul stanza already present — skipping"
fi
NOMAD_CONSUL

  echo "==> Verifying Consul is up on ${SSH_ADDR}..."
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "consul members 2>/dev/null || echo 'consul members failed — may take a moment to join'" 2>&1

  echo ""
  echo "  ✓ Consul client deployed on ${SSH_USER}@${SSH_ADDR}"
done

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Consul client deployment complete."
echo ""
echo "  Verify from aither:"
echo "    consul members"
echo "══════════════════════════════════════════════════════"
