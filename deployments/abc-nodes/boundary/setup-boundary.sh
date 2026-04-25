#!/usr/bin/env bash
# setup-boundary.sh
#
# Post-deploy Boundary configuration:
#   • Creates an org scope and project scope
#   • Generates an SSH keypair and stores the private key in Vault KV
#   • Registers abc-nodes cluster nodes as SSH targets
#   • Wires up a Vault KV SSH credential library so Boundary brokers the key on demand
#
# NOTE: Boundary 0.18.2 OSS does not support SSH-type targets (HCP only).
# We use TCP targets (port 22) with a vault-generic credential library of
# credential_type=ssh_private_key. Boundary fetches the private key from Vault
# KV and brokers it back to the client during session authorization.
#
# The SSH CA flow (sign user keys) requires Boundary HCP or a newer OSS version
# that supports injected credentials + SSH targets.
#
# Prerequisites
# ─────────────
#   1. Vault is running (vault.service on sun-aither)
#   2. Boundary controller is running (boundary-controller.service on sun-aither)
#      and boundary database init has completed
#   3. acl/boundary-credentials.env contains auth-method-id and admin password
#   4. acl/vault-keys.env contains VAULT_ROOT_TOKEN
#   5. Target node accepts SSH pubkey auth (we install the generated pubkey)
#   6. curl, python3, ssh-keygen in PATH
#
# Usage
# ─────
#   bash deployments/abc-nodes/boundary/setup-boundary.sh
#
#   With overrides:
#   BOUNDARY_ADDR=http://100.70.185.46:9200 \
#   TARGET_USER=ubuntu \
#   bash deployments/abc-nodes/boundary/setup-boundary.sh
#
# What it creates
# ───────────────
#   Vault:
#     KV v2 mount: ssh-creds/
#     Secret:      ssh-creds/data/aither  → {username, private_key}
#     Policy:      boundary-ssh-controller (read KV, revoke leases, renew token)
#     Token:       orphan, periodic, for Boundary credential store
#
#   Boundary:
#   Global scope
#   └── org: "abc-nodes-org"
#       └── project: "abc-nodes-ssh"
#           ├── host-catalog:  "abc-nodes-hosts"
#           │   └── host: aither (100.70.185.46)
#           ├── host-set:      "abc-nodes-all-hosts"
#           ├── credential-store: "vault-ssh-ca" (Vault credential store)
#           │   └── credential-library: "aither-ssh-key"
#           │       (vault-generic, credential_type=ssh_private_key)
#           └── target: "aither-ssh"
#               ├── host-set: abc-nodes-all-hosts
#               └── brokered credential: aither-ssh-key
#
# User workflow (after this script runs)
# ──────────────────────────────────────
#   boundary authenticate password \
#     -addr=${BOUNDARY_ADDR} \
#     -auth-method-id=<ampw_xxx> \
#     -login-name=admin \
#     -password <printed-at-init>
#
#   boundary connect ssh \
#     -addr=${BOUNDARY_ADDR} \
#     -target-id=<ttcp_xxx> \
#     -username=<TARGET_USER>
#
#   Boundary will broker the SSH private key from Vault and inject it into the
#   SSH handshake. No manual key management required.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BOUNDARY_ADDR="${BOUNDARY_ADDR:-http://100.70.185.46:9200}"
VAULT_ADDR="${VAULT_ADDR:-http://100.70.185.46:8200}"

CREDS_FILE="${CREDS_FILE:-${DEPLOY_ROOT}/acl/boundary-credentials.env}"
KEYS_FILE="${KEYS_FILE:-${DEPLOY_ROOT}/acl/vault-keys.env}"

# The user that Boundary will SSH as on the target node.
# Default: current local user (who has SSH access to the node).
TARGET_USER="${TARGET_USER:-${USER}}"
# Tailscale IP of the primary node.
AITHER_TS_IP="${AITHER_TS_IP:-100.70.185.46}"

echo "==> Boundary + Vault SSH setup"
echo "    BOUNDARY_ADDR: ${BOUNDARY_ADDR}"
echo "    VAULT_ADDR:    ${VAULT_ADDR}"
echo "    TARGET_USER:   ${TARGET_USER}"
echo "    AITHER_TS_IP:  ${AITHER_TS_IP}"
echo ""

# ── Check prerequisites ───────────────────────────────────────────────────────
command -v curl      >/dev/null 2>&1 || { echo "ERROR: curl not in PATH" >&2; exit 1; }
command -v python3   >/dev/null 2>&1 || { echo "ERROR: python3 not in PATH" >&2; exit 1; }
command -v ssh-keygen>/dev/null 2>&1 || { echo "ERROR: ssh-keygen not in PATH" >&2; exit 1; }

[[ -f "${CREDS_FILE}" ]] || {
  echo "ERROR: ${CREDS_FILE} not found." >&2
  echo "  Contains BOUNDARY_AUTH_METHOD_ID, BOUNDARY_ADMIN_PASSWORD" >&2
  exit 1
}
[[ -f "${KEYS_FILE}" ]] || {
  echo "ERROR: ${KEYS_FILE} not found." >&2
  exit 1
}

# shellcheck source=/dev/null
source "${CREDS_FILE}"
# shellcheck source=/dev/null
source "${KEYS_FILE}"

AUTH_METHOD_ID="${BOUNDARY_AUTH_METHOD_ID:-}"
ADMIN_PASSWORD="${BOUNDARY_ADMIN_PASSWORD:-}"
VAULT_TOKEN="${VAULT_ROOT_TOKEN:-}"

[[ -n "${AUTH_METHOD_ID}" ]] || { echo "ERROR: BOUNDARY_AUTH_METHOD_ID not set in ${CREDS_FILE}" >&2; exit 1; }
[[ -n "${ADMIN_PASSWORD}" ]] || { echo "ERROR: BOUNDARY_ADMIN_PASSWORD not set in ${CREDS_FILE}" >&2; exit 1; }
[[ -n "${VAULT_TOKEN}" ]]    || { echo "ERROR: VAULT_ROOT_TOKEN not set in ${KEYS_FILE}" >&2; exit 1; }

# ── Helper: Boundary HTTP auth ─────────────────────────────────────────────────
bnd_auth() {
  local resp
  resp=$(curl -s -X POST "${BOUNDARY_ADDR}/v1/auth-methods/${AUTH_METHOD_ID}:authenticate" \
    -H "Content-Type: application/json" \
    -d "{\"attributes\":{\"login_name\":\"admin\",\"password\":\"${ADMIN_PASSWORD}\"}}")
  echo "${resp}" | python3 -c "import sys,json; print(json.load(sys.stdin)['attributes']['token'])"
}

# ── Helper: Boundary API call ──────────────────────────────────────────────────
bnd_get() {
  local path="$1" token="$2"
  curl -s -H "Authorization: Bearer ${token}" "${BOUNDARY_ADDR}/v1/${path}"
}

bnd_post() {
  local path="$1" token="$2" body="$3"
  curl -s -X POST "${BOUNDARY_ADDR}/v1/${path}" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

# ── Helper: Vault API call ─────────────────────────────────────────────────────
vault_put() {
  local path="$1" body="$2"
  curl -s -X PUT "${VAULT_ADDR}/v1/${path}" \
    -H "X-Vault-Token: ${VAULT_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

vault_post() {
  local path="$1" body="$2"
  curl -s -X POST "${VAULT_ADDR}/v1/${path}" \
    -H "X-Vault-Token: ${VAULT_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

# ── Authenticate to Boundary ───────────────────────────────────────────────────
echo "==> Authenticating to Boundary..."
BTOKEN=$(bnd_auth)
echo "    OK"
echo ""

# ── [1] Generate SSH keypair ───────────────────────────────────────────────────
KEY_DIR="${DEPLOY_ROOT}/acl/.boundary-ssh"
mkdir -p "${KEY_DIR}"
chmod 700 "${KEY_DIR}"

KEY_FILE="${KEY_DIR}/id_ed25519"
if [[ -f "${KEY_FILE}" ]]; then
  echo "==> [1] SSH keypair already exists at ${KEY_FILE} — reusing."
else
  echo "==> [1] Generating SSH keypair for Boundary-brokered access..."
  ssh-keygen -t ed25519 -C "boundary-brokered@abc-nodes" -f "${KEY_FILE}" -N "" -q
  echo "    Generated: ${KEY_FILE}"
fi
PUBKEY=$(cat "${KEY_FILE}.pub")
echo "    Public key: ${PUBKEY}"
echo ""

# ── [2] Install public key on target nodes ────────────────────────────────────
echo "==> [2] Installing public key on ${TARGET_USER}@${AITHER_TS_IP}..."
echo "    Add this to ${AITHER_TS_IP}:~${TARGET_USER}/.ssh/authorized_keys:"
echo "    ${PUBKEY}"
echo ""
echo "    To install automatically (if you have SSH access):"
echo "      ssh ${TARGET_USER}@${AITHER_TS_IP} \"mkdir -p ~/.ssh && echo '${PUBKEY}' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys\""
echo ""
echo "    Attempting auto-install..."
if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 \
       "${TARGET_USER}@${AITHER_TS_IP}" \
       "grep -q '$(echo "${PUBKEY}" | awk '{print $2}')' ~/.ssh/authorized_keys 2>/dev/null && echo already_present || (mkdir -p ~/.ssh && echo '${PUBKEY}' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && echo installed)" 2>/dev/null | grep -qE 'installed|already_present'; then
  echo "    Public key installed ✓"
else
  echo "    WARNING: Auto-install failed. Install the public key manually before testing."
fi
echo ""

# ── [3] Store private key in Vault KV ─────────────────────────────────────────
echo "==> [3] Enabling Vault KV v2 at ssh-creds/..."
KV_MOUNTS=$(curl -s -H "X-Vault-Token: ${VAULT_TOKEN}" "${VAULT_ADDR}/v1/sys/mounts")
if echo "${KV_MOUNTS}" | python3 -c "import sys,json; d=json.load(sys.stdin); exit(0 if 'ssh-creds/' in d else 1)" 2>/dev/null; then
  echo "    Already enabled."
else
  vault_post "sys/mounts/ssh-creds" '{"type":"kv","options":{"version":"2"}}' >/dev/null
  echo "    Enabled ssh-creds/ KV v2."
fi

echo "==> [4] Storing SSH private key in Vault at ssh-creds/data/aither..."
JSON_PAYLOAD=$(python3 -c "
import json
privkey = open('${KEY_FILE}').read()
print(json.dumps({'data': {'username': '${TARGET_USER}', 'private_key': privkey}}))
")
STORE_RESP=$(curl -s -X POST "${VAULT_ADDR}/v1/ssh-creds/data/aither" \
  -H "X-Vault-Token: ${VAULT_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "${JSON_PAYLOAD}")
echo "    Version: $(echo "${STORE_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('data',{}).get('version','?'))" 2>/dev/null)"
echo ""

# ── [5] Vault policy for Boundary controller ───────────────────────────────────
echo "==> [5] Writing Vault policy 'boundary-ssh-controller'..."
POLICY_BODY=$(python3 -c "
import json
policy = '''# Boundary SSH credential controller policy
path \"auth/token/lookup-self\" { capabilities = [\"read\"] }
path \"auth/token/renew-self\" { capabilities = [\"update\"] }
path \"auth/token/revoke-self\" { capabilities = [\"update\"] }
path \"sys/leases/revoke\" { capabilities = [\"update\"] }
path \"ssh-creds/data/*\" { capabilities = [\"read\"] }
path \"ssh-client-signer/config/ca\" { capabilities = [\"read\"] }
path \"ssh-client-signer/sign/ssh-role\" { capabilities = [\"create\", \"update\"] }
'''
print(json.dumps({'policy': policy}))
")
vault_put "sys/policies/acl/boundary-ssh-controller" "${POLICY_BODY}" >/dev/null
echo "    Policy written."

echo "==> [6] Creating orphan Vault token for Boundary credential store..."
TOKEN_RESP=$(vault_post "auth/token/create-orphan" \
  '{"policies":["boundary-ssh-controller"],"ttl":"720h","renewable":true,"period":"720h","display_name":"boundary-ssh-controller"}')
BND_VAULT_TOKEN=$(echo "${TOKEN_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin)['auth']['client_token'])")
echo "    Boundary Vault token created."
echo ""

# ── [7] Boundary scopes ────────────────────────────────────────────────────────
echo "==> [7] Creating Boundary org scope: abc-nodes-org..."
ORG_RESP=$(bnd_post "scopes" "${BTOKEN}" \
  '{"scope_id":"global","name":"abc-nodes-org","description":"abc-nodes cluster organization"}')
ORG_ID=$(echo "${ORG_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${ORG_ID}" ]]; then
  ORG_ID=$(bnd_get "scopes?scope_id=global" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='abc-nodes-org']")
fi
echo "    org: ${ORG_ID}"

echo "==> [8] Creating project scope: abc-nodes-ssh..."
PROJ_RESP=$(bnd_post "scopes" "${BTOKEN}" \
  "{\"scope_id\":\"${ORG_ID}\",\"name\":\"abc-nodes-ssh\",\"description\":\"SSH access to abc-nodes cluster\"}")
PROJECT_ID=$(echo "${PROJ_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${PROJECT_ID}" ]]; then
  PROJECT_ID=$(bnd_get "scopes?scope_id=${ORG_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='abc-nodes-ssh']")
fi
echo "    project: ${PROJECT_ID}"
echo ""

# ── [8] Host catalog + host + host-set ────────────────────────────────────────
echo "==> [9] Creating static host catalog: abc-nodes-hosts..."
HC_RESP=$(bnd_post "host-catalogs" "${BTOKEN}" \
  "{\"scope_id\":\"${PROJECT_ID}\",\"type\":\"static\",\"name\":\"abc-nodes-hosts\",\"description\":\"abc-nodes cluster hosts\"}")
HC_ID=$(echo "${HC_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${HC_ID}" ]]; then
  HC_ID=$(bnd_get "host-catalogs?scope_id=${PROJECT_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='abc-nodes-hosts']")
fi
echo "    catalog: ${HC_ID}"

echo "==> [10] Registering host: aither (${AITHER_TS_IP})..."
HOST_RESP=$(bnd_post "hosts" "${BTOKEN}" \
  "{\"host_catalog_id\":\"${HC_ID}\",\"type\":\"static\",\"name\":\"aither\",\"description\":\"Primary node — sun-aither\",\"attributes\":{\"address\":\"${AITHER_TS_IP}\"}}")
AITHER_HOST_ID=$(echo "${HOST_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${AITHER_HOST_ID}" ]]; then
  AITHER_HOST_ID=$(bnd_get "hosts?host_catalog_id=${HC_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='aither']")
fi
echo "    host: ${AITHER_HOST_ID}"

echo "==> [11] Creating host-set: abc-nodes-all-hosts..."
HS_RESP=$(bnd_post "host-sets" "${BTOKEN}" \
  "{\"host_catalog_id\":\"${HC_ID}\",\"type\":\"static\",\"name\":\"abc-nodes-all-hosts\",\"description\":\"All abc-nodes cluster hosts\"}")
HS_ID=$(echo "${HS_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${HS_ID}" ]]; then
  HS_ID=$(bnd_get "host-sets?host_catalog_id=${HC_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='abc-nodes-all-hosts']")
fi

# Add host to host-set
HS_VER=$(bnd_get "host-sets/${HS_ID}" "${BTOKEN}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('version',1))" 2>/dev/null || echo 1)
bnd_post "host-sets/${HS_ID}:add-hosts" "${BTOKEN}" \
  "{\"host_ids\":[\"${AITHER_HOST_ID}\"],\"version\":${HS_VER}}" >/dev/null
echo "    host-set: ${HS_ID} (aither added)"
echo ""

# ── [9] Vault credential store ─────────────────────────────────────────────────
echo "==> [12] Creating Vault credential store: vault-ssh-ca..."
CS_RESP=$(bnd_post "credential-stores" "${BTOKEN}" \
  "{\"scope_id\":\"${PROJECT_ID}\",\"type\":\"vault\",\"name\":\"vault-ssh-ca\",\"description\":\"Vault KV SSH credential store\",\"attributes\":{\"address\":\"${VAULT_ADDR}\",\"token\":\"${BND_VAULT_TOKEN}\"}}")
CS_ID=$(echo "${CS_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${CS_ID}" ]]; then
  CS_ID=$(bnd_get "credential-stores?scope_id=${PROJECT_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='vault-ssh-ca']")
fi
echo "    credential-store: ${CS_ID}"

echo "==> [13] Creating credential library: aither-ssh-key..."
CL_RESP=$(bnd_post "credential-libraries" "${BTOKEN}" \
  "{\"credential_store_id\":\"${CS_ID}\",\"type\":\"vault-generic\",\"credential_type\":\"ssh_private_key\",\"name\":\"aither-ssh-key\",\"description\":\"SSH private key for ${TARGET_USER}@aither from Vault KV\",\"attributes\":{\"path\":\"ssh-creds/data/aither\",\"http_method\":\"GET\"}}")
CL_ID=$(echo "${CL_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${CL_ID}" ]]; then
  # List libraries from the store
  CL_ID=$(bnd_get "credential-libraries?credential_store_id=${CS_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='aither-ssh-key']" 2>/dev/null || echo "")
fi
echo "    credential-library: ${CL_ID}"
echo ""

# ── [10] TCP target ────────────────────────────────────────────────────────────
# NOTE: Boundary 0.18.2 OSS doesn't support SSH-type targets.
# TCP target + brokered vault-generic credential library achieves the same result:
# boundary connect ssh will broker the key and use it for the SSH session.
echo "==> [14] Creating TCP target (port 22): aither-ssh..."
TGT_RESP=$(bnd_post "targets" "${BTOKEN}" \
  "{\"scope_id\":\"${PROJECT_ID}\",\"type\":\"tcp\",\"name\":\"aither-ssh\",\"description\":\"SSH access to sun-aither via Vault-brokered SSH key\",\"attributes\":{\"default_port\":22}}")
TGT_ID=$(echo "${TGT_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('id',''))" 2>/dev/null)
if [[ -z "${TGT_ID}" ]]; then
  TGT_ID=$(bnd_get "targets?scope_id=${PROJECT_ID}" "${BTOKEN}" | \
    python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); [print(i['id']) for i in items if i.get('name')=='aither-ssh']")
fi
echo "    target: ${TGT_ID}"

echo "==> [15] Wiring host-set and credential library to target..."
TGT_VER=$(bnd_get "targets/${TGT_ID}" "${BTOKEN}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('version',1))" 2>/dev/null || echo 1)

bnd_post "targets/${TGT_ID}:add-host-sources" "${BTOKEN}" \
  "{\"host_source_ids\":[\"${HS_ID}\"],\"version\":${TGT_VER}}" >/dev/null

TGT_VER=$(bnd_get "targets/${TGT_ID}" "${BTOKEN}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('item',{}).get('version',1))" 2>/dev/null || echo 1)

bnd_post "targets/${TGT_ID}:add-credential-sources" "${BTOKEN}" \
  "{\"brokered_credential_source_ids\":[\"${CL_ID}\"],\"version\":${TGT_VER}}" >/dev/null
echo "    Host-set and credential library attached."
echo ""

# ── Summary ───────────────────────────────────────────────────────────────────
cat <<SUMMARY
══════════════════════════════════════════════════════
  Boundary + Vault SSH configured!

  Resource IDs:
    Org:               ${ORG_ID}
    Project:           ${PROJECT_ID}
    Host catalog:      ${HC_ID}
    Host (aither):     ${AITHER_HOST_ID}
    Host-set:          ${HS_ID}
    Credential store:  ${CS_ID}
    Credential lib:    ${CL_ID}
    Target (TCP/22):   ${TGT_ID}

  SSH keypair (Boundary-brokered):
    Private key: ${KEY_FILE}
    Public key:  ${KEY_FILE}.pub
    (Public key must be in ${TARGET_USER}@aither:~/.ssh/authorized_keys)

  User workflow:
    boundary authenticate password \\
      -addr=${BOUNDARY_ADDR} \\
      -auth-method-id=${AUTH_METHOD_ID} \\
      -login-name=admin

    boundary connect ssh \\
      -addr=${BOUNDARY_ADDR} \\
      -target-id=${TGT_ID} \\
      -username=${TARGET_USER}

  Or via HTTP API:
    curl -X POST ${BOUNDARY_ADDR}/v1/targets/${TGT_ID}:authorize-session \\
      -H "Authorization: Bearer \$BTOKEN" \\
      -H "Content-Type: application/json" -d '{}'

  Add more nodes:
    1. Add the public key to the new node's authorized_keys
    2. Store credentials in Vault: vault kv put ssh-creds/node2 username=ubuntu private_key=@...
    3. Register host in Boundary and add to host-set ${HS_ID}
    4. Create a new target or reuse the existing one (aither-ssh) and expand host-set

  Boundary UI: http://${AITHER_TS_IP}:9200
  Vault UI:    http://${AITHER_TS_IP}:8200
══════════════════════════════════════════════════════
SUMMARY
