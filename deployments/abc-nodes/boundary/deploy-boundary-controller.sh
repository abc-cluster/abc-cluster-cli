#!/usr/bin/env bash
set -euo pipefail

# deploy-boundary-controller.sh
#
# Install HashiCorp Boundary controller as a systemd service on sun-aither.
# Follows the same pattern as deploy-consul.sh and deploy-vault.sh.
#
# What this script does (on the remote host):
#   1. Downloads and installs the Boundary binary
#   2. Creates the boundary system user and required directories
#   3. Generates /etc/boundary.d/controller.hcl with KMS keys + DB URL embedded
#   4. Registers two Consul services (API :9200 and cluster :9201)
#   5. Installs and starts boundary-controller.service
#   6. Runs `boundary database init` on first deploy (idempotent)
#   7. Smoke-tests health endpoint and Consul registration
#
# Prerequisites
# ─────────────
#  • SSH access to sun-aither
#  • acl/boundary-controller.env must exist (created by bootstrap-secrets.sh)
#  • PostgreSQL running and accessible at DB_HOST:DB_PORT
#  • Vault is running (needed later for SSH CA, not for controller itself)
#
# Migration from Nomad job
# ────────────────────────
#  Stop the old controller job first:
#    abc admin services nomad cli -- job stop -namespace=abc-services -purge abc-nodes-boundary-controller
#  Then run this script. PostgreSQL data is preserved.
#
# Usage
# ─────
#   bash deployments/abc-nodes/boundary/deploy-boundary-controller.sh
#
#   Override:
#     BOUNDARY_VERSION=0.18.2 DB_HOST=100.70.185.46 ./deploy-boundary-controller.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"
BOUNDARY_VERSION="${BOUNDARY_VERSION:-0.18.2}"
AITHER_TS_IP="${AITHER_TS_IP:-100.70.185.46}"

# Database — must match postgres.nomad.hcl
DB_HOST="${DB_HOST:-100.70.185.46}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-wave}"
DB_PASSWORD="${DB_PASSWORD:-wave_db_secret}"
DB_NAME="${DB_NAME:-boundary}"

ENV_FILE="${ENV_FILE:-${DEPLOY_ROOT}/acl/boundary-controller.env}"
LOCAL_SERVICE="${SCRIPT_DIR}/boundary-controller.service"
LOCAL_CONSUL_JSON="${SCRIPT_DIR}/consul-boundary-controller.json"

# ── Validate ──────────────────────────────────────────────────────────────────
for f in "${ENV_FILE}" "${LOCAL_SERVICE}" "${LOCAL_CONSUL_JSON}"; do
  [[ -f "$f" ]] || { echo "ERROR: required file not found: $f" >&2; exit 1; }
done

# Load KMS keys
# shellcheck source=/dev/null
source "${ENV_FILE}"

for v in BOUNDARY_ROOT_KEY BOUNDARY_WORKER_AUTH_KEY BOUNDARY_RECOVERY_KEY; do
  [[ -n "${!v:-}" ]] || { echo "ERROR: ${v} not set in ${ENV_FILE}" >&2; exit 1; }
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

echo "==> Boundary controller deploy → ${REMOTE_HOST}"
echo "    Version:   ${BOUNDARY_VERSION}"
echo "    DB host:   ${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo "    Env file:  ${ENV_FILE}"
echo ""

# ── Build the remote setup script ─────────────────────────────────────────────
# KMS keys and DB URL are embedded here (bash variable expansion) so the
# generated controller.hcl on the server contains literal values — no template
# engine required on the host side. Same technique as deploy-consul.sh.
SETUP_SCRIPT="$(mktemp /tmp/boundary-setup-XXXX.sh)"
trap 'rm -f "${SETUP_SCRIPT}"' EXIT

cat > "${SETUP_SCRIPT}" <<SETUP
#!/usr/bin/env bash
set -euo pipefail

BOUNDARY_VERSION="${BOUNDARY_VERSION}"
AITHER_TS_IP="${AITHER_TS_IP}"
DB_URL="postgresql://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
BOUNDARY_ROOT_KEY="${BOUNDARY_ROOT_KEY}"
BOUNDARY_WORKER_AUTH_KEY="${BOUNDARY_WORKER_AUTH_KEY}"
BOUNDARY_RECOVERY_KEY="${BOUNDARY_RECOVERY_KEY}"

# ── [1/7] Install Boundary binary ─────────────────────────────────────────────
echo "==> [1/7] Installing Boundary \${BOUNDARY_VERSION}..."
if command -v boundary &>/dev/null && boundary version 2>/dev/null | grep -q "\${BOUNDARY_VERSION}"; then
  echo "    Already at \${BOUNDARY_VERSION} — skipping download"
else
  cd /tmp
  ARCH=\$(dpkg --print-architecture 2>/dev/null || echo amd64)
  curl -fsSL "https://releases.hashicorp.com/boundary/\${BOUNDARY_VERSION}/boundary_\${BOUNDARY_VERSION}_linux_\${ARCH}.zip" \
    -o boundary.zip
  unzip -o boundary.zip boundary
  install -o root -g root -m 0755 boundary /usr/local/bin/boundary
  rm -f boundary boundary.zip
  echo "    Boundary \$(boundary version | head -1) installed"
fi

# ── [2/7] Create boundary user and directories ────────────────────────────────
echo "==> [2/7] Creating boundary user and directories..."
if ! id boundary &>/dev/null; then
  useradd --system --home /etc/boundary.d --shell /bin/false boundary
  echo "    Created boundary system user"
else
  echo "    boundary user already exists"
fi
mkdir -p /etc/boundary.d
chown boundary:boundary /etc/boundary.d
chmod 750 /etc/boundary.d

# ── [3/7] Ensure boundary database exists ─────────────────────────────────────
echo "==> [3/7] Ensuring boundary database exists..."
if ! command -v psql >/dev/null 2>&1; then
  echo "    Installing postgresql-client..."
  apt-get install -y -q postgresql-client 2>&1 | tail -3
fi
export PGPASSWORD="${DB_PASSWORD}"
EXISTS=\$(psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d postgres \
  -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" 2>/dev/null || echo "")
if [ "\${EXISTS}" = "1" ]; then
  echo "    Database '${DB_NAME}' already exists."
else
  echo "    Creating database '${DB_NAME}'..."
  psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d postgres \
    -c "CREATE DATABASE ${DB_NAME};"
  echo "    Done."
fi

# ── [4/7] Generate controller.hcl with embedded secrets ──────────────────────
echo "==> [4/7] Writing /etc/boundary.d/controller.hcl..."
cat > /etc/boundary.d/controller.hcl <<'HCLEOF'
# Generated by deploy-boundary-controller.sh — do not edit by hand.
# To regenerate: re-run the deploy script after updating acl/boundary-controller.env.
disable_mlock = true

controller {
  name        = "abc-nodes-boundary-controller"
  description = "abc-nodes Boundary controller — manages SSH session brokering"

  database {
    url = "BOUNDARY_DB_URL_PLACEHOLDER"
  }
}

listener "tcp" {
  address     = "0.0.0.0:9200"
  purpose     = "api"
  tls_disable = true
}

listener "tcp" {
  address     = "0.0.0.0:9201"
  purpose     = "cluster"
  tls_disable = true
}

listener "tcp" {
  address     = "0.0.0.0:9202"
  purpose     = "proxy"
  tls_disable = true
}

kms "aead" {
  purpose   = "root"
  aead_type = "aes-gcm"
  key       = "BOUNDARY_ROOT_KEY_PLACEHOLDER"
  key_id    = "global_root"
}

kms "aead" {
  purpose   = "worker-auth"
  aead_type = "aes-gcm"
  key       = "BOUNDARY_WORKER_AUTH_KEY_PLACEHOLDER"
  key_id    = "global_worker_auth"
}

kms "aead" {
  purpose   = "recovery"
  aead_type = "aes-gcm"
  key       = "BOUNDARY_RECOVERY_KEY_PLACEHOLDER"
  key_id    = "global_recovery"
}
HCLEOF

# Substitute placeholders with actual values using python (handles any chars safely)
python3 - <<PYEOF
import re, os
path = '/etc/boundary.d/controller.hcl'
content = open(path).read()
content = content.replace('BOUNDARY_DB_URL_PLACEHOLDER',         os.environ.get('_DB_URL', ''))
content = content.replace('BOUNDARY_ROOT_KEY_PLACEHOLDER',       os.environ.get('_ROOT', ''))
content = content.replace('BOUNDARY_WORKER_AUTH_KEY_PLACEHOLDER',os.environ.get('_WAUTH', ''))
content = content.replace('BOUNDARY_RECOVERY_KEY_PLACEHOLDER',   os.environ.get('_RECOV', ''))
open(path, 'w').write(content)
PYEOF

chown boundary:boundary /etc/boundary.d/controller.hcl
chmod 640 /etc/boundary.d/controller.hcl
echo "    Done"

# ── [5/7] Register Consul services ────────────────────────────────────────────
echo "==> [5/7] Registering Boundary in Consul..."
cp /tmp/consul-boundary-controller.json /etc/consul.d/boundary-controller.json
chown consul:consul /etc/consul.d/boundary-controller.json
chmod 640 /etc/consul.d/boundary-controller.json
if systemctl is-active --quiet consul; then
  consul reload
  echo "    Consul reloaded — boundary-controller and boundary-cluster registered"
else
  echo "    WARNING: Consul is not running"
fi

# ── [6/7] Database init (must run BEFORE starting the service) ────────────────
# Boundary cannot start against an uninitialized database. Run init first;
# on subsequent deploys this is a no-op (exits 0 with "already initialized").
echo "==> [6/7] Running boundary database init (before service start)..."
cp /tmp/boundary-controller.service /etc/systemd/system/boundary-controller.service
systemctl daemon-reload
# Stop any lingering instance before init
systemctl stop boundary-controller 2>/dev/null || true

INIT_OUT=\$(boundary database init \
  -config=/etc/boundary.d/controller.hcl \
  2>&1) && INIT_RC=0 || INIT_RC=\$?

echo "\${INIT_OUT}"

if echo "\${INIT_OUT}" | grep -q "Login Name"; then
  echo ""
  echo "  *** FIRST RUN — save these credentials! ***"
  CREDS_FILE="/etc/boundary.d/initial-credentials.txt"
  echo "\${INIT_OUT}" > "\${CREDS_FILE}"
  chmod 600 "\${CREDS_FILE}"
  echo "  Saved to: \${CREDS_FILE}"
fi

# ── [7/7] Install and start systemd service ───────────────────────────────────
echo "==> [7/7] Starting boundary-controller.service..."
systemctl enable boundary-controller
systemctl start boundary-controller
sleep 4
systemctl is-active boundary-controller || {
  echo "ERROR: boundary-controller.service failed to start"
  journalctl -u boundary-controller -n 40 --no-pager
  exit 1
}
echo "    boundary-controller.service is active"

echo ""
echo "══════════════ Smoke tests ══════════════"

echo "--- health endpoint ---"
curl -sf http://127.0.0.1:9200/health && echo "  ✓ /health OK" || echo "  WARN: /health check failed"

echo ""
echo "--- Consul service checks ---"
curl -sf "http://127.0.0.1:8500/v1/health/service/abc-nodes-boundary-controller?passing=true" \
  | python3 -c "import sys,json; svcs=json.load(sys.stdin); print('  passing:', len(svcs), 'instance(s)')" \
  2>/dev/null || echo "  (pending — Consul check takes up to 15 s)"

echo ""
echo "==> Boundary UI:    http://\${AITHER_TS_IP}:9200"
echo "==> boundary.aither (via Traefik): http://boundary.aither"
echo "==> Setup complete."
SETUP

# Export secret values so the python substitution step can read them as env vars.
# We pass them to the remote setup script via the env that sudo inherits.
# Since sudo -S strips the env by default, we prefix the script with exports.
SETUP_WITH_ENV="$(mktemp /tmp/boundary-setup-env-XXXX.sh)"
trap 'rm -f "${SETUP_SCRIPT}" "${SETUP_WITH_ENV}"' EXIT

cat > "${SETUP_WITH_ENV}" <<ENVWRAP
#!/usr/bin/env bash
set -euo pipefail
export _DB_URL="postgresql://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
export _ROOT="${BOUNDARY_ROOT_KEY}"
export _WAUTH="${BOUNDARY_WORKER_AUTH_KEY}"
export _RECOV="${BOUNDARY_RECOVERY_KEY}"
bash /tmp/boundary-setup.sh
ENVWRAP

# ── Transfer files ────────────────────────────────────────────────────────────
echo "==> Transferring files to ${REMOTE_HOST}..."
${SCP} "${LOCAL_SERVICE}"      "${REMOTE_HOST}:/tmp/boundary-controller.service"
${SCP} "${LOCAL_CONSUL_JSON}"  "${REMOTE_HOST}:/tmp/consul-boundary-controller.json"
${SCP} "${SETUP_SCRIPT}"       "${REMOTE_HOST}:/tmp/boundary-setup.sh"
${SCP} "${SETUP_WITH_ENV}"     "${REMOTE_HOST}:/tmp/boundary-setup-env.sh"

# ── Run on remote ─────────────────────────────────────────────────────────────
echo "==> Running setup on ${REMOTE_HOST}..."
if [[ -n "${PASS}" ]]; then
  echo "${PASS}" | ${SSH} "sudo -SE bash /tmp/boundary-setup-env.sh"
else
  ${SSH} "sudo -E bash /tmp/boundary-setup-env.sh"
fi

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Boundary controller systemd service deployed."
echo ""
echo "  Manage with:"
echo "    systemctl status boundary-controller"
echo "    journalctl -u boundary-controller -f"
echo ""
echo "  Initial credentials (if first run):"
echo "    ssh ${REMOTE_HOST} sudo cat /etc/boundary.d/initial-credentials.txt"
echo ""
echo "  Next steps:"
echo "    # Register SSH targets:"
echo "    export BOUNDARY_ADDR=http://boundary.aither"
echo "    boundary authenticate password -auth-method-id=<ampw_xxx> -login-name=admin"
echo "    bash deployments/abc-nodes/boundary/setup-boundary.sh"
echo "══════════════════════════════════════════════════════"
