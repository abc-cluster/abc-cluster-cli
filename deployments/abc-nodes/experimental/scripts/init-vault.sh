#!/usr/bin/env bash
# init-vault.sh
#
# Initialize and unseal Vault on abc-nodes (aither).
# Safe to re-run: detects existing initialization and skips init, only unseals.
#
# Prerequisites:
#   Network access to 100.70.185.46:8200 (Tailscale)
#   python3  — for JSON parsing
#
# Usage:
#   bash deployments/abc-nodes/experimental/scripts/init-vault.sh
#
# Outputs:
#   deployments/abc-nodes/experimental/acl/vault-keys.env  ← KEEP SECRET, never commit
#
# AUTO-UNSEAL NOTE:
#   Vault re-seals on every process restart. Re-run this script after
#   node reboots or Nomad restarts the Vault allocation. For production,
#   configure auto-unseal via Transit secret engine or cloud KMS.

set -euo pipefail

VAULT_ADDR="${VAULT_ADDR:-http://100.70.185.46:8200}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KEYS_FILE="${SCRIPT_DIR}/../acl/vault-keys.env"

echo "==> Vault addr: ${VAULT_ADDR}"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Check connectivity
# ─────────────────────────────────────────────────────────────────────────────
if ! curl -sf "${VAULT_ADDR}/v1/sys/health?uninitcode=200&sealedcode=200" >/dev/null; then
  echo "ERROR: Cannot reach Vault at ${VAULT_ADDR}" >&2
  echo "  Is the abc-nodes-vault Nomad job running?" >&2
  echo "  Check: abc admin services nomad cli -- job status abc-nodes-vault" >&2
  exit 1
fi

# ─────────────────────────────────────────────────────────────────────────────
# Check initialization status
# ─────────────────────────────────────────────────────────────────────────────
INIT_STATUS=$(curl -sf "${VAULT_ADDR}/v1/sys/init" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['initialized'])")

if [[ "${INIT_STATUS}" == "True" ]]; then
  echo "==> Vault is already initialized."

  SEAL_STATUS=$(curl -sf "${VAULT_ADDR}/v1/sys/seal-status" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['sealed'])")

  if [[ "${SEAL_STATUS}" == "False" ]]; then
    echo "    Vault is already unsealed — nothing to do."
    echo ""
    echo "    Root token: source ${KEYS_FILE} && echo \$VAULT_ROOT_TOKEN"
    exit 0
  fi

  echo "    Vault is sealed — unsealing with saved keys..."
  if [[ ! -f "${KEYS_FILE}" ]]; then
    echo "ERROR: Vault is sealed but keys file not found at ${KEYS_FILE}" >&2
    echo "  You must manually provide unseal keys." >&2
    exit 1
  fi
  # shellcheck source=/dev/null
  source "${KEYS_FILE}"

  for key_var in VAULT_UNSEAL_KEY_1 VAULT_UNSEAL_KEY_2 VAULT_UNSEAL_KEY_3; do
    key="${!key_var}"
    result=$(curl -sf -X POST "${VAULT_ADDR}/v1/sys/unseal" \
      -H "Content-Type: application/json" \
      -d "{\"key\": \"${key}\"}")
    sealed=$(echo "${result}" | python3 -c "import sys,json; print(json.load(sys.stdin)['sealed'])")
    progress=$(echo "${result}" | python3 -c "import sys,json; d=json.load(sys.stdin); print(str(d['progress'])+'/'+str(d['t']))")
    echo "    Key applied — progress: ${progress}, sealed: ${sealed}"
  done
  echo ""
  echo "==> Vault unsealed."
  exit 0
fi

# ─────────────────────────────────────────────────────────────────────────────
# Initialize Vault (5 key shares, threshold 3)
# ─────────────────────────────────────────────────────────────────────────────
echo "==> Initializing Vault (5 shares, threshold 3)..."
INIT_RESPONSE=$(curl -sf -X POST "${VAULT_ADDR}/v1/sys/init" \
  -H "Content-Type: application/json" \
  -d '{"secret_shares": 5, "secret_threshold": 3}')

ROOT_TOKEN=$(echo "${INIT_RESPONSE}" | python3 -c "import sys,json; print(json.load(sys.stdin)['root_token'])")
readarray -t KEYS_B64 < <(echo "${INIT_RESPONSE}" | \
  python3 -c "import sys,json; [print(k) for k in json.load(sys.stdin)['keys_base64']]")

echo "    Initialization complete."
echo "    Root token: ${ROOT_TOKEN}"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Save keys to file
# ─────────────────────────────────────────────────────────────────────────────
touch "${KEYS_FILE}"; chmod 600 "${KEYS_FILE}"
{
  echo "# Vault unseal keys — KEEP SECRET, do not commit"
  echo "# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "export VAULT_ADDR=${VAULT_ADDR}"
  echo "export VAULT_ROOT_TOKEN=${ROOT_TOKEN}"
  for i in "${!KEYS_B64[@]}"; do
    echo "export VAULT_UNSEAL_KEY_$((i+1))=${KEYS_B64[$i]}"
  done
} > "${KEYS_FILE}"

echo "    Unseal keys + root token saved to: ${KEYS_FILE}"
echo "    *** KEEP THIS FILE PRIVATE — never commit it ***"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Unseal Vault (apply first 3 of 5 keys)
# ─────────────────────────────────────────────────────────────────────────────
echo "==> Unsealing Vault (applying 3 of 5 keys)..."
for i in 0 1 2; do
  result=$(curl -sf -X POST "${VAULT_ADDR}/v1/sys/unseal" \
    -H "Content-Type: application/json" \
    -d "{\"key\": \"${KEYS_B64[$i]}\"}")
  sealed=$(echo "${result}" | python3 -c "import sys,json; print(json.load(sys.stdin)['sealed'])")
  progress=$(echo "${result}" | python3 -c "import sys,json; d=json.load(sys.stdin); print(str(d['progress'])+'/'+str(d['t']))")
  echo "    Key $((i+1)) applied — progress: ${progress}, sealed: ${sealed}"
done

echo ""
echo "==================================================================="
echo " Vault initialized and unsealed!"
echo ""
echo " Root token: ${ROOT_TOKEN}"
echo " Keys file:  ${KEYS_FILE}"
echo ""
echo " Recommended next steps:"
echo "   1. Enable KV secrets engine:"
echo "      VAULT_TOKEN=${ROOT_TOKEN} vault secrets enable -path=secret kv-v2"
echo ""
echo "   2. Store the root token in Nomad Variables for safe keeping:"
echo "      abc admin services nomad cli -- var put -namespace services -force \\"
echo "        nomad/jobs/abc-nodes-vault vault_root_token=${ROOT_TOKEN}"
echo ""
echo "   3. Create a restricted Vault policy for service accounts (optional):"
echo "      vault policy write abc-nodes-readonly - <<EOF"
echo "      path \"secret/data/abc-nodes/*\" { capabilities = [\"read\"] }"
echo "      EOF"
echo ""
echo " IMPORTANT: Vault re-seals on restart."
echo "   Re-run this script (just unseals, won't reinitialize) after:"
echo "   - Node reboots"
echo "   - Nomad restarts the vault allocation"
echo "==================================================================="
