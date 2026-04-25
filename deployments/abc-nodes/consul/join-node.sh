#!/usr/bin/env bash
set -euo pipefail

# Join a new worker node (node2, node3, …) to the abc-nodes Consul + Nomad cluster.
#
# What this script does on the remote node:
#   1. Installs the Consul binary (same version as the server)
#   2. Creates the consul system user and required directories
#   3. Installs consul-client.hcl and the systemd unit (patched with node IP)
#   4. Starts the Consul client agent and verifies it joined the cluster
#   5. Installs dnsmasq and configures Consul + *.aither DNS (mirrors deploy-consul.sh)
#   6. Installs the Nomad client config drop-in that points at the Consul agent
#   7. Smoke-tests that `consul members` shows both nodes
#
# Prerequisites
# ─────────────
#  • Tailscale must already be running on the new node (tailscaled active, IP assigned).
#  • The SERVER (aither) must already be running Consul (deploy-consul.sh was run).
#  • Nomad must already be installed on the new node (client mode config present).
#  • If gossip encryption is enabled: set the same key in consul-client.hcl before running.
#
# Usage
# ─────
#  REMOTE_HOST=sun-node2 NODE_TS_IP=100.x.x.x ./join-node.sh
#
# Override any default via environment:
#   CONSUL_VERSION=1.19.2 REMOTE_HOST=sun-node2 NODE_TS_IP=100.x.x.x ./join-node.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

REMOTE_HOST="${REMOTE_HOST:-sun-node2}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.${REMOTE_HOST}}"
CONSUL_VERSION="${CONSUL_VERSION:-1.19.2}"

# Tailscale IP of the NEW node — required, must be set explicitly.
NODE_TS_IP="${NODE_TS_IP:?ERROR: set NODE_TS_IP to the new node's Tailscale IP}"

# Server (aither) addresses — used for DNS resolution of *.aither.
SERVER_TS_IP="${SERVER_TS_IP:-100.70.185.46}"
AITHER_LAN_IP="${AITHER_LAN_IP:-146.232.174.77}"

LOCAL_CLIENT_HCL="${SCRIPT_DIR}/consul-client.hcl"
LOCAL_CONSUL_SERVICE="${SCRIPT_DIR}/consul.service"

if [[ ! -f "${PASS_FILE}" ]]; then
  echo "ERROR: password file not found: ${PASS_FILE}" >&2
  echo "       Create it or set PASS_FILE=/path/to/file" >&2
  exit 1
fi

for f in "${LOCAL_CLIENT_HCL}" "${LOCAL_CONSUL_SERVICE}"; do
  [[ -f "$f" ]] || { echo "ERROR: not found: $f" >&2; exit 1; }
done

SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
PASS="$(cat "${PASS_FILE}")"

# ── Patch consul-client.hcl with the node's actual Tailscale IP ──────────────
PATCHED_HCL="$(mktemp /tmp/consul-client-XXXX.hcl)"
trap 'rm -f "${PATCHED_HCL}" "${SETUP_SCRIPT}"' EXIT

sed "s/NODE_TAILSCALE_IP/${NODE_TS_IP}/g" "${LOCAL_CLIENT_HCL}" > "${PATCHED_HCL}"

# ── Generate the remote setup script ─────────────────────────────────────────
SETUP_SCRIPT="$(mktemp /tmp/consul-join-setup-XXXX.sh)"

cat > "${SETUP_SCRIPT}" <<SETUP
#!/usr/bin/env bash
set -euo pipefail

CONSUL_VERSION="${CONSUL_VERSION}"
NODE_TS_IP="${NODE_TS_IP}"
SERVER_TS_IP="${SERVER_TS_IP}"
AITHER_LAN_IP="${AITHER_LAN_IP}"

echo "==> [1/6] Installing Consul \${CONSUL_VERSION}..."
if command -v consul &>/dev/null && consul version 2>/dev/null | grep -q "\${CONSUL_VERSION}"; then
  echo "    Already at \${CONSUL_VERSION} — skipping download"
else
  cd /tmp
  ARCH=\$(dpkg --print-architecture 2>/dev/null || echo amd64)
  curl -fsSL "https://releases.hashicorp.com/consul/\${CONSUL_VERSION}/consul_\${CONSUL_VERSION}_linux_\${ARCH}.zip" \
    -o consul.zip
  unzip -o consul.zip consul
  install -o root -g root -m 0755 consul /usr/local/bin/consul
  rm -f consul consul.zip
  echo "    Consul \$(consul version | head -1) installed"
fi

echo "==> [2/6] Creating consul user and directories..."
if ! id consul &>/dev/null; then
  useradd --system --home /etc/consul.d --shell /bin/false consul
  echo "    Created consul system user"
else
  echo "    consul user already exists"
fi
mkdir -p /etc/consul.d /opt/consul /var/log/consul
chown -R consul:consul /etc/consul.d /opt/consul /var/log/consul
chmod 750 /etc/consul.d /opt/consul

echo "==> [3/6] Installing client config and systemd unit..."
cp /tmp/consul-client.hcl  /etc/consul.d/consul.hcl
cp /tmp/consul.service     /etc/systemd/system/consul.service
chown consul:consul /etc/consul.d/consul.hcl
chmod 640 /etc/consul.d/consul.hcl
systemctl daemon-reload
echo "    Done"

echo "==> [4/6] Starting Consul client..."
systemctl enable consul
if systemctl is-active --quiet consul; then
  systemctl reload consul 2>/dev/null || systemctl restart consul
  echo "    Reloaded"
else
  systemctl start consul
  echo "    Started"
fi
sleep 4
systemctl is-active consul || { echo "ERROR: consul failed to start"; journalctl -u consul -n 30 --no-pager; exit 1; }
echo "    Consul client active — checking cluster membership..."
consul members || { echo "ERROR: consul members failed — check retry_join address"; exit 1; }

echo "==> [5/6] Configuring dnsmasq..."
apt-get install -y -qq dnsmasq

cat > /etc/dnsmasq.d/10-consul.conf <<'DNS'
# Forward all *.consul queries to Consul DNS on port 8600.
server=/consul/127.0.0.1#8600
no-negcache
DNS

cat > /etc/dnsmasq.d/20-aither.conf <<DNS
# Resolve *.aither to aither's LAN IP (Caddy entry point).
address=/.aither/\${AITHER_LAN_IP}
DNS

cat > /etc/dnsmasq.d/00-listen.conf <<'DNS'
listen-address=127.0.0.1
bind-interfaces
DNS

UPSTREAM=\$(systemd-resolve --status 2>/dev/null \
  | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' \
  || echo "8.8.8.8")
echo "    Upstream DNS: \${UPSTREAM}"

cat > /etc/dnsmasq.d/30-upstream.conf <<DNS
server=\${UPSTREAM}
DNS

# Break the resolv.conf symlink so dnsmasq owns DNS.
mkdir -p /etc/systemd/resolved.conf.d
cat > /etc/systemd/resolved.conf.d/no-stub.conf <<'RCONF'
[Resolve]
DNSStubListener=no
RCONF

systemctl restart systemd-resolved 2>/dev/null || true
systemctl enable dnsmasq
systemctl restart dnsmasq
sleep 2
systemctl is-active dnsmasq || { echo "ERROR: dnsmasq failed"; journalctl -u dnsmasq -n 20 --no-pager; exit 1; }

# Break the symlink and write a static resolv.conf pointing at dnsmasq.
rm -f /etc/resolv.conf
cat > /etc/resolv.conf <<'RESOLV'
# Managed by join-node.sh — points to local dnsmasq.
# dnsmasq: .consul → Consul DNS:8600, *.aither → LAN IP, rest → upstream.
nameserver 127.0.0.1
RESOLV
chattr +i /etc/resolv.conf
echo "    /etc/resolv.conf updated (immutable)"

echo "==> [6/6] Nomad consul stanza..."
NOMAD_CONSUL_DROP="/etc/nomad.d/consul.hcl"
if [[ ! -f "\${NOMAD_CONSUL_DROP}" ]]; then
  cat > "\${NOMAD_CONSUL_DROP}" <<'NCONSUL'
# Consul integration — written by join-node.sh
consul {
  address = "127.0.0.1:8500"
}
NCONSUL
  echo "    Written to \${NOMAD_CONSUL_DROP}"
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
  sleep 3
else
  echo "    Already present — skipping"
fi

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Smoke tests"
echo "══════════════════════════════════════════════════════"
echo ""
echo "--- consul members (expect both aither + this node) ---"
consul members

echo ""
echo "--- DNS: consul.service.consul ---"
if dig @127.0.0.1 -p 8600 consul.service.consul +short 2>/dev/null | grep -q '.'; then
  echo "  ✓ consul.service.consul resolves"
else
  echo "  WARNING: consul.service.consul did not resolve"
fi

echo ""
echo "--- DNS: grafana.aither ---"
if host grafana.aither 127.0.0.1 2>/dev/null | grep -q "\${AITHER_LAN_IP}"; then
  echo "  ✓ grafana.aither → \${AITHER_LAN_IP}"
else
  echo "  WARNING: grafana.aither did not resolve via dnsmasq"
fi

echo ""
echo "==> Node joined successfully."
echo "    NOTE: This node should NOT have the 'scratch' host volume configured"
echo "    in its Nomad client config (/etc/nomad.d/client.hcl) unless you"
echo "    explicitly want stateful services (Grafana, MinIO, Postgres) to"
echo "    be schedulable here. By default they stay pinned to aither."
SETUP

# ── Transfer files ────────────────────────────────────────────────────────────
echo "==> Transferring config files to ${REMOTE_HOST}..."
${SCP} "${PATCHED_HCL}"          "${REMOTE_HOST}:/tmp/consul-client.hcl"
${SCP} "${LOCAL_CONSUL_SERVICE}" "${REMOTE_HOST}:/tmp/consul.service"
${SCP} "${SETUP_SCRIPT}"         "${REMOTE_HOST}:/tmp/consul-join-setup.sh"

echo "==> Running join setup on ${REMOTE_HOST} (this takes ~1 min)..."
echo "${PASS}" | ${SSH} "sudo -S bash /tmp/consul-join-setup.sh"
