#!/usr/bin/env bash
# setup-ssh-ca.sh
#
# Configure Vault SSH CA and signing role for abc-nodes.
#
# What this does
# ──────────────
#  1. Enables the SSH secrets engine at "ssh-client-signer"
#  2. Generates a Vault-internal SSH CA key pair
#  3. Creates a signing role ("ssh-role") that allows signing user SSH public keys
#  4. Prints the CA public key (add to target nodes via configure-node.sh)
#  5. Creates a Vault policy ("ssh-signer") so non-root tokens can sign keys
#
# User workflow after this script runs
# ─────────────────────────────────────
#  export VAULT_ADDR=http://vault.aither
#  export VAULT_TOKEN=<user-token>          # token with ssh-signer policy
#
#  # Sign your SSH public key (valid for 1 hour)
#  vault write -field=signed_key ssh-client-signer/sign/ssh-role \
#    public_key=@~/.ssh/id_rsa.pub \
#    valid_principals="ubuntu,ec2-user,abhi" \
#  > ~/.ssh/signed-cert.pub
#
#  # SSH using the signed certificate
#  ssh -i ~/.ssh/id_rsa -i ~/.ssh/signed-cert.pub ubuntu@<node-ip>
#
# Prerequisites
# ─────────────
#  vault CLI in PATH, VAULT_ADDR set, VAULT_TOKEN set (root or admin token)
#
# Usage
# ─────
#   export VAULT_ADDR=http://vault.aither
#   export VAULT_TOKEN=<root-token>
#   bash deployments/abc-nodes/vault/setup-ssh-ca.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VAULT_ADDR="${VAULT_ADDR:-http://vault.aither}"
VAULT_TOKEN="${VAULT_TOKEN:-}"
SSH_MOUNT="${SSH_MOUNT:-ssh-client-signer}"
SSH_ROLE="${SSH_ROLE:-ssh-role}"

# Default valid principals: common usernames on cluster nodes.
# Extend with a comma-separated list if needed.
VALID_PRINCIPALS="${VALID_PRINCIPALS:-ubuntu,abhi,researcher}"

# Certificate TTL for user-signed keys.
DEFAULT_TTL="${DEFAULT_TTL:-1h}"
MAX_TTL="${MAX_TTL:-8h}"

export VAULT_ADDR
export VAULT_TOKEN

echo "==> Vault SSH CA setup"
echo "    VAULT_ADDR:        ${VAULT_ADDR}"
echo "    SSH mount path:    ${SSH_MOUNT}"
echo "    Role name:         ${SSH_ROLE}"
echo "    Valid principals:  ${VALID_PRINCIPALS}"
echo "    Default TTL:       ${DEFAULT_TTL} (max: ${MAX_TTL})"
echo ""

# ── Check prerequisites ───────────────────────────────────────────────────────
command -v vault >/dev/null 2>&1 || { echo "ERROR: vault not in PATH" >&2; exit 1; }
[[ -n "${VAULT_TOKEN}" ]] || { echo "ERROR: VAULT_TOKEN not set" >&2; exit 1; }

# Verify Vault is reachable and unsealed
STATUS=$(vault status -format=json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['sealed'])")
if [[ "${STATUS}" == "True" ]] || [[ "${STATUS}" == "true" ]]; then
  echo "ERROR: Vault is sealed. Run experimental/scripts/init-vault.sh first." >&2
  exit 1
fi
echo "    Vault is unsealed ✓"
echo ""

# ── 1. Enable SSH secrets engine ─────────────────────────────────────────────
echo "==> 1. Enabling SSH secrets engine at ${SSH_MOUNT}/"
if vault secrets list -format=json | python3 -c "import sys,json; print('${SSH_MOUNT}/' in json.load(sys.stdin))" | grep -q True; then
  echo "    Already enabled — skipping."
else
  vault secrets enable -path="${SSH_MOUNT}" ssh
  echo "    Enabled."
fi
echo ""

# ── 2. Configure SSH CA (generate key pair inside Vault) ─────────────────────
echo "==> 2. Configuring SSH CA key pair"
CA_CHECK=$(vault read -field=public_key "${SSH_MOUNT}/config/ca" 2>/dev/null || echo "")
if [[ -n "${CA_CHECK}" ]]; then
  echo "    CA already configured — using existing key pair."
else
  vault write "${SSH_MOUNT}/config/ca" generate_signing_key=true
  echo "    CA key pair generated."
fi
echo ""

# ── 3. Create signing role ────────────────────────────────────────────────────
echo "==> 3. Creating signing role '${SSH_ROLE}'"
vault write "${SSH_MOUNT}/roles/${SSH_ROLE}" \
  key_type=ca \
  allowed_users="${VALID_PRINCIPALS}" \
  default_user="ubuntu" \
  ttl="${DEFAULT_TTL}" \
  max_ttl="${MAX_TTL}" \
  allow_user_certificates=true \
  allowed_extensions="permit-pty,permit-port-forwarding" \
  default_extensions="permit-pty= permit-port-forwarding="
echo "    Role created."
echo ""

# ── 4. Create a non-root policy for users ────────────────────────────────────
echo "==> 4. Creating Vault policy 'ssh-signer' for user tokens"
vault policy write ssh-signer - <<'POLICY'
# ssh-signer — allows signing user SSH public keys via the SSH CA.
# Assign this policy to tokens that users will use with `vault write`.
path "ssh-client-signer/sign/ssh-role" {
  capabilities = ["create", "update"]
}

path "ssh-client-signer/config/ca" {
  capabilities = ["read"]
}
POLICY
echo "    Policy 'ssh-signer' written."
echo ""

# ── 5. Retrieve and display CA public key ─────────────────────────────────────
echo "==> 5. Vault SSH CA public key"
CA_PUBLIC_KEY=$(vault read -field=public_key "${SSH_MOUNT}/config/ca")
echo ""
echo "${CA_PUBLIC_KEY}"
echo ""

# Save to a local file for use with configure-node.sh
CA_FILE="${SCRIPT_DIR}/vault_ssh_ca.pub"
echo "${CA_PUBLIC_KEY}" > "${CA_FILE}"
echo "    Saved to: ${CA_FILE}"
echo ""

# ── Summary ───────────────────────────────────────────────────────────────────
cat <<SUMMARY
══════════════════════════════════════════════════════
  Vault SSH CA configured!

  CA public key saved to: ${CA_FILE}

  Next steps:

  1. Trust the CA on each target SSH host:
       bash deployments/abc-nodes/vault/configure-node.sh \\
         --node sun-aither
       bash deployments/abc-nodes/vault/configure-node.sh \\
         --node sun-node2

  2. Create a user token with ssh-signer policy:
       vault token create -policy=ssh-signer -ttl=24h

  3. Users sign their SSH key:
       export VAULT_ADDR=http://vault.aither
       export VAULT_TOKEN=<user-token>
       vault write -field=signed_key \\
         ${SSH_MOUNT}/sign/${SSH_ROLE} \\
         public_key=@~/.ssh/id_rsa.pub \\
         valid_principals="ubuntu" \\
       > ~/.ssh/signed-cert.pub

  4. SSH with the signed certificate:
       ssh -i ~/.ssh/id_rsa -i ~/.ssh/signed-cert.pub ubuntu@<node-ip>

  For Boundary integration (session brokering):
       bash deployments/abc-nodes/boundary/setup-boundary.sh
══════════════════════════════════════════════════════
SUMMARY
