#!/usr/bin/env bash
set -euo pipefail

# Deploy Consul + dnsmasq to sun-aither.
#
# What this script does (on the remote host):
#   1. Downloads and installs the Consul binary
#   2. Creates the consul system user and required directories
#   3. Installs consul.hcl config and the systemd unit
#   4. Starts and enables the Consul service
#   5. Installs dnsmasq and configures:
#        a. Forward *.consul → Consul DNS (127.0.0.1:8600)
#        b. Resolve *.aither → aither's LAN IP (146.232.174.77)
#        c. Disable systemd-resolved stub so dnsmasq owns port 53
#   6. Adds a consul { } stanza to Nomad's config
#   7. Smoke-tests that `consul members` shows the server healthy
#
# DNS for other clients
# ─────────────────────
#  After this script aither itself resolves *.aither and *.consul correctly.
#  For tailnet devices: Tailscale admin console → DNS → add nameserver
#  100.70.185.46 restricted to domain ".aither".
#
# Override defaults via environment variables:
#   CONSUL_VERSION=1.19.2 REMOTE_HOST=sun-aither ./deploy-consul.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"
CONSUL_VERSION="${CONSUL_VERSION:-1.19.2}"
AITHER_LAN_IP="${AITHER_LAN_IP:-146.232.174.77}"
AITHER_TS_IP="${AITHER_TS_IP:-100.70.185.46}"

LOCAL_CONSUL_HCL="${SCRIPT_DIR}/consul.hcl"
LOCAL_CONSUL_SERVICE="${SCRIPT_DIR}/consul.service"

if [[ ! -f "${PASS_FILE}" ]]; then
  echo "ERROR: password file not found: ${PASS_FILE}" >&2
  exit 1
fi

SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
PASS="$(cat "${PASS_FILE}")"

# ── Generate the remote setup script locally, then ship and run it ────────────
# Strategy: write setup.sh to a local temp file → scp → run with password piped.
# This avoids the stdin conflict between heredoc and sudo -S.

SETUP_SCRIPT="$(mktemp /tmp/consul-setup-XXXX.sh)"
trap 'rm -f "${SETUP_SCRIPT}"' EXIT

cat > "${SETUP_SCRIPT}" <<SETUP
#!/usr/bin/env bash
set -euo pipefail

CONSUL_VERSION="${CONSUL_VERSION}"
AITHER_LAN_IP="${AITHER_LAN_IP}"
AITHER_TS_IP="${AITHER_TS_IP}"

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

echo "==> [3/6] Installing config and systemd unit..."
cp /tmp/consul.hcl     /etc/consul.d/consul.hcl
cp /tmp/consul.service /etc/systemd/system/consul.service
chown consul:consul /etc/consul.d/consul.hcl
chmod 640 /etc/consul.d/consul.hcl
systemctl daemon-reload
echo "    Done"

echo "==> [4/6] Starting Consul..."
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

echo "==> [5/6] Configuring dnsmasq..."
apt-get install -y -qq dnsmasq

cat > /etc/dnsmasq.d/10-consul.conf <<'DNS'
# Forward all *.consul queries to Consul DNS on port 8600.
server=/consul/127.0.0.1#8600
no-negcache
DNS

cat > /etc/dnsmasq.d/20-aither.conf <<DNS
# Resolve *.aither to aither's LAN IP.
# Caddy binds on this IP for all vhost routes.
address=/.aither/\${AITHER_LAN_IP}
DNS

cat > /etc/dnsmasq.d/00-listen.conf <<'DNS'
# Listen only on loopback (containers and remote clients use the host IP).
listen-address=127.0.0.1
bind-interfaces
DNS

# Detect current upstream DNS before we disable systemd-resolved stub.
UPSTREAM=\$(systemd-resolve --status 2>/dev/null \
  | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' \
  || echo "8.8.8.8")
echo "    Upstream DNS: \${UPSTREAM}"

cat > /etc/dnsmasq.d/30-upstream.conf <<DNS
server=\${UPSTREAM}
DNS

# Disable systemd-resolved stub listener so dnsmasq can own port 53.
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

# Point host resolver at dnsmasq (127.0.0.1).
# Use chattr -i in case resolv.conf is immutable (some cloud images lock it).
chattr -i /etc/resolv.conf 2>/dev/null || true
cat > /etc/resolv.conf <<'RESOLV'
# Managed by deploy-consul.sh — points to local dnsmasq.
# dnsmasq: .consul → Consul DNS:8600, *.aither → LAN IP, rest → upstream.
nameserver 127.0.0.1
RESOLV
echo "    /etc/resolv.conf updated"

echo "==> [6/6] Nomad consul stanza..."
NOMAD_CONSUL_DROP="/etc/nomad.d/consul.hcl"
if [[ ! -f "\${NOMAD_CONSUL_DROP}" ]]; then
  cat > "\${NOMAD_CONSUL_DROP}" <<'NCONSUL'
# Consul integration — written by deploy-consul.sh
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
echo "══════════════ Smoke tests ══════════════"
echo "--- consul members ---"
consul members

echo ""
echo "--- DNS: consul.service.consul via port 8600 ---"
if dig @127.0.0.1 -p 8600 consul.service.consul +short 2>/dev/null | grep -q '.'; then
  echo "  ✓ consul.service.consul resolves"
else
  echo "  WARNING: consul.service.consul did not resolve — check consul logs"
fi

echo ""
echo "--- DNS: grafana.aither via dnsmasq ---"
if host grafana.aither 127.0.0.1 2>/dev/null | grep -q "\${AITHER_LAN_IP}"; then
  echo "  ✓ grafana.aither → \${AITHER_LAN_IP}"
else
  echo "  WARNING: grafana.aither did not resolve — check dnsmasq"
fi

echo ""
echo "==> Consul UI: http://\${AITHER_TS_IP}:8500/ui"
echo "==> Setup complete."
SETUP

echo "==> Transferring config files to ${REMOTE_HOST}..."
${SCP} "${LOCAL_CONSUL_HCL}"     "${REMOTE_HOST}:/tmp/consul.hcl"
${SCP} "${LOCAL_CONSUL_SERVICE}" "${REMOTE_HOST}:/tmp/consul.service"
${SCP} "${SETUP_SCRIPT}"         "${REMOTE_HOST}:/tmp/consul-setup.sh"

echo "==> Running setup on ${REMOTE_HOST} (this takes ~1 min)..."
# Pipe the password to sudo -S via stdin; the script itself is a file arg.
echo "${PASS}" | ${SSH} "sudo -S bash /tmp/consul-setup.sh"
