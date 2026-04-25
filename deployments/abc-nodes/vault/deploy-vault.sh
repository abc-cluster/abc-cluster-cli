#!/usr/bin/env bash
set -euo pipefail

# deploy-vault.sh
#
# Install HashiCorp Vault as a systemd service on sun-aither.
# Follows the same pattern as deploy-consul.sh.
#
# What this script does (on the remote host):
#   1. Downloads and installs the Vault binary
#   2. Creates the vault system user and required directories
#   3. Installs vault.hcl config and unseal.sh helper
#   4. Writes /etc/vault.d/vault.env from acl/vault-keys.env (chmod 600)
#   5. Registers the Consul service (consul-vault.json → /etc/consul.d/)
#   6. Installs and starts vault.service
#   7. Migrates Raft data from the old Nomad path if present
#   8. Smoke-tests that Vault is unsealed and reachable
#
# Migration from Nomad job
# ────────────────────────
#  Stop the old job first (the script will warn if it detects port 8200 in use):
#    abc admin services nomad cli -- job stop -namespace=abc-services -purge abc-nodes-vault
#  Then run this script.
#
# Prerequisites
# ─────────────
#  • SSH access to sun-aither (sshpass + PASS_FILE, or key-based)
#  • acl/vault-keys.env must exist (created by experimental/scripts/init-vault.sh)
#  • openssl in PATH (for generating a test token)
#
# Usage
# ─────
#   bash deployments/abc-nodes/vault/deploy-vault.sh
#
#   Override defaults:
#     VAULT_VERSION=1.18.3 REMOTE_HOST=sun-aither ./deploy-vault.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"
VAULT_VERSION="${VAULT_VERSION:-1.18.3}"
VAULT_DATA_DIR="${VAULT_DATA_DIR:-/opt/vault/data}"
NOMAD_VAULT_DATA_DIR="${NOMAD_VAULT_DATA_DIR:-/opt/nomad/vault/data}"
AITHER_TS_IP="${AITHER_TS_IP:-100.70.185.46}"

KEYS_FILE="${KEYS_FILE:-${DEPLOY_ROOT}/acl/vault-keys.env}"
LOCAL_VAULT_HCL="${SCRIPT_DIR}/vault.hcl"
LOCAL_VAULT_SERVICE="${SCRIPT_DIR}/vault.service"
LOCAL_UNSEAL_SH="${SCRIPT_DIR}/unseal.sh"
LOCAL_CONSUL_JSON="${SCRIPT_DIR}/consul-vault.json"

# ── Validate local prerequisites ──────────────────────────────────────────────
for f in "${KEYS_FILE}" "${LOCAL_VAULT_HCL}" "${LOCAL_VAULT_SERVICE}" "${LOCAL_UNSEAL_SH}" "${LOCAL_CONSUL_JSON}"; do
  [[ -f "$f" ]] || { echo "ERROR: required file not found: $f" >&2; exit 1; }
done

# Load unseal keys from vault-keys.env
# shellcheck source=/dev/null
source "${KEYS_FILE}"

for v in VAULT_UNSEAL_KEY_1 VAULT_UNSEAL_KEY_2 VAULT_UNSEAL_KEY_3; do
  [[ -n "${!v:-}" ]] || { echo "ERROR: ${v} not set in ${KEYS_FILE}" >&2; exit 1; }
done

# ── SSH helpers ───────────────────────────────────────────────────────────────
if [[ -f "${PASS_FILE}" ]]; then
  SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
  SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
  PASS="$(cat "${PASS_FILE}")"
else
  SSH="ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
  SCP="scp -o StrictHostKeyChecking=no"
  PASS=""
fi

echo "==> Vault deploy → ${REMOTE_HOST}"
echo "    Version:     ${VAULT_VERSION}"
echo "    Data dir:    ${VAULT_DATA_DIR}"
echo "    Keys file:   ${KEYS_FILE}"
echo ""

# ── Build vault.env for the server (no 'export' prefix — systemd EnvironmentFile format) ──
VAULT_ENV_CONTENT="# Vault unseal keys — written by deploy-vault.sh
# Managed by systemd EnvironmentFile= in vault.service
# chmod 600, root:root — never readable by the vault process directly
VAULT_UNSEAL_KEY_1=${VAULT_UNSEAL_KEY_1}
VAULT_UNSEAL_KEY_2=${VAULT_UNSEAL_KEY_2}
VAULT_UNSEAL_KEY_3=${VAULT_UNSEAL_KEY_3}
"

# ── Build remote setup script ─────────────────────────────────────────────────
SETUP_SCRIPT="$(mktemp /tmp/vault-setup-XXXX.sh)"
trap 'rm -f "${SETUP_SCRIPT}"' EXIT

cat > "${SETUP_SCRIPT}" <<SETUP
#!/usr/bin/env bash
set -euo pipefail

VAULT_VERSION="${VAULT_VERSION}"
VAULT_DATA_DIR="${VAULT_DATA_DIR}"
NOMAD_VAULT_DATA_DIR="${NOMAD_VAULT_DATA_DIR}"
AITHER_TS_IP="${AITHER_TS_IP}"

# ── [1/7] Install Vault binary ────────────────────────────────────────────────
echo "==> [1/7] Installing Vault \${VAULT_VERSION}..."
if command -v vault &>/dev/null && vault version 2>/dev/null | grep -q "\${VAULT_VERSION}"; then
  echo "    Already at \${VAULT_VERSION} — skipping download"
else
  cd /tmp
  ARCH=\$(dpkg --print-architecture 2>/dev/null || echo amd64)
  curl -fsSL "https://releases.hashicorp.com/vault/\${VAULT_VERSION}/vault_\${VAULT_VERSION}_linux_\${ARCH}.zip" \
    -o vault.zip
  unzip -o vault.zip vault
  install -o root -g root -m 0755 vault /usr/local/bin/vault
  rm -f vault vault.zip
  echo "    Vault \$(vault version | head -1) installed"
fi

# ── [2/7] Create vault user and directories ───────────────────────────────────
echo "==> [2/7] Creating vault user and directories..."
if ! id vault &>/dev/null; then
  useradd --system --home /etc/vault.d --shell /bin/false vault
  echo "    Created vault system user"
else
  echo "    vault user already exists"
fi
mkdir -p /etc/vault.d "\${VAULT_DATA_DIR}"
chown vault:vault /etc/vault.d "\${VAULT_DATA_DIR}"
chmod 750 /etc/vault.d "\${VAULT_DATA_DIR}"

# ── [3/7] Migrate Raft data from old Nomad path (if present) ─────────────────
echo "==> [3/7] Checking for Nomad-era Raft data migration..."
if [[ -d "\${NOMAD_VAULT_DATA_DIR}" && ! -f "\${VAULT_DATA_DIR}/vault.db" ]]; then
  echo "    Migrating \${NOMAD_VAULT_DATA_DIR} → \${VAULT_DATA_DIR} ..."
  cp -a "\${NOMAD_VAULT_DATA_DIR}/." "\${VAULT_DATA_DIR}/"
  chown -R vault:vault "\${VAULT_DATA_DIR}"
  echo "    Migration complete. Old path left in place for safety."
  echo "    Remove manually when confirmed working: rm -rf \${NOMAD_VAULT_DATA_DIR}"
elif [[ -f "\${VAULT_DATA_DIR}/vault.db" ]]; then
  echo "    Existing Raft data found at \${VAULT_DATA_DIR} — no migration needed."
else
  echo "    No existing data — fresh install."
fi

# ── [4/7] Install config files ────────────────────────────────────────────────
echo "==> [4/7] Installing config files..."
cp /tmp/vault.hcl      /etc/vault.d/vault.hcl
cp /tmp/unseal.sh      /etc/vault.d/unseal.sh
cp /tmp/vault.env      /etc/vault.d/vault.env
cp /tmp/vault.service  /etc/systemd/system/vault.service

chown vault:vault /etc/vault.d/vault.hcl /etc/vault.d/unseal.sh
chmod 640  /etc/vault.d/vault.hcl
chmod 750  /etc/vault.d/unseal.sh

# vault.env: only root reads this (systemd reads it as root, passes vars to processes)
chown root:root /etc/vault.d/vault.env
chmod 600  /etc/vault.d/vault.env

systemctl daemon-reload
echo "    Done"

# ── [5/7] Register Consul service ─────────────────────────────────────────────
echo "==> [5/7] Registering Vault in Consul..."
cp /tmp/consul-vault.json /etc/consul.d/vault.json
chown consul:consul /etc/consul.d/vault.json
chmod 640 /etc/consul.d/vault.json
# Ask Consul to reload its config directory
if systemctl is-active --quiet consul; then
  consul reload
  echo "    Consul reloaded — vault service registered"
else
  echo "    WARNING: Consul is not running; service will register on next consul start"
fi

# ── [6/7] Start vault.service ─────────────────────────────────────────────────
echo "==> [6/7] Starting vault.service..."
systemctl enable vault
if systemctl is-active --quiet vault; then
  echo "    Vault already running — restarting to pick up new config..."
  systemctl restart vault
else
  systemctl start vault
fi
# Give the unseal ExecStartPost time to complete
sleep 6
systemctl is-active vault || {
  echo "ERROR: vault.service failed to start"
  journalctl -u vault -n 40 --no-pager
  exit 1
}
echo "    vault.service is active"

# ── [7/7] Smoke tests ─────────────────────────────────────────────────────────
echo ""
echo "══════════════ Smoke tests ══════════════"

echo "--- vault status ---"
VAULT_ADDR="http://127.0.0.1:8200" vault status 2>/dev/null || true

echo ""
echo "--- Consul service check ---"
curl -sf "http://127.0.0.1:8500/v1/health/service/abc-nodes-vault?passing=true" \
  | python3 -c "import sys,json; svcs=json.load(sys.stdin); print('  passing services:', len(svcs)); [print('   ',s['Service']['ID']) for s in svcs]" \
  2>/dev/null || echo "  (Consul check pending — may take up to 15 s)"

echo ""
echo "==> Vault UI: http://\${AITHER_TS_IP}:8200/ui"
echo "==> vault.aither (via Traefik): http://vault.aither/ui/"
echo "==> Setup complete."
SETUP

# ── Transfer files to remote ──────────────────────────────────────────────────
echo "==> Transferring files to ${REMOTE_HOST}..."
${SCP} "${LOCAL_VAULT_HCL}"      "${REMOTE_HOST}:/tmp/vault.hcl"
${SCP} "${LOCAL_VAULT_SERVICE}"  "${REMOTE_HOST}:/tmp/vault.service"
${SCP} "${LOCAL_UNSEAL_SH}"      "${REMOTE_HOST}:/tmp/unseal.sh"
${SCP} "${LOCAL_CONSUL_JSON}"    "${REMOTE_HOST}:/tmp/consul-vault.json"
${SCP} "${SETUP_SCRIPT}"         "${REMOTE_HOST}:/tmp/vault-setup.sh"

# Write vault.env locally as a temp file, scp it, then shred it
VAULT_ENV_TMP="$(mktemp /tmp/vault-XXXX.env)"
trap 'rm -f "${SETUP_SCRIPT}" "${VAULT_ENV_TMP}"' EXIT
printf '%s' "${VAULT_ENV_CONTENT}" > "${VAULT_ENV_TMP}"
${SCP} "${VAULT_ENV_TMP}"        "${REMOTE_HOST}:/tmp/vault.env"

# ── Run setup on remote ───────────────────────────────────────────────────────
echo "==> Running setup on ${REMOTE_HOST}..."
if [[ -n "${PASS}" ]]; then
  echo "${PASS}" | ${SSH} "sudo -S bash /tmp/vault-setup.sh"
else
  ${SSH} "sudo bash /tmp/vault-setup.sh"
fi

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Vault systemd service deployed."
echo ""
echo "  Manage with:"
echo "    systemctl status vault"
echo "    journalctl -u vault -f"
echo ""
echo "  Next steps (if not already done):"
echo "    # Configure SSH CA:"
echo "    export VAULT_ADDR=http://vault.aither"
echo "    export VAULT_TOKEN=\${VAULT_ROOT_TOKEN}  # from acl/vault-keys.env"
echo "    bash deployments/abc-nodes/vault/setup-ssh-ca.sh"
echo ""
echo "    # Then deploy Boundary controller:"
echo "    bash deployments/abc-nodes/boundary/deploy-boundary-controller.sh"
echo "══════════════════════════════════════════════════════"
