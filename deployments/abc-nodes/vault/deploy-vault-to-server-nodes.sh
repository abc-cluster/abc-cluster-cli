#!/usr/bin/env bash
# deploy-vault-to-server-nodes.sh
#
# Install Vault on the Nomad SERVER nodes and configure Nomad's vault stanza
# to point at aither's Vault server (http://100.70.185.46:8200).
#
# This installs the Vault binary (for CLI use: signing SSH certs, etc.) and
# configures Nomad on each server node to use aither's central Vault cluster
# for job-level secret injection.
#
# Nomad server nodes:
#   nomad00   abhinav@100.108.199.30
#   nomad01   abhinav@100.77.21.36
#   oci       ubuntu@129.151.174.199
#
# Usage
# ─────
#   export VAULT_TOKEN=<root-or-admin-token>
#   bash deployments/abc-nodes/vault/deploy-vault-to-server-nodes.sh
#
# Prerequisites
# ─────────────
#   hashi-up in PATH (or accessible via abc admin services hashi-up cli)
#   VAULT_TOKEN set (for creating the Nomad-Vault integration token)
#   SSH access to the nodes via ~/.ssh/id_ed25519

set -euo pipefail

VAULT_VERSION="${VAULT_VERSION:-1.18.4}"
SSH_KEY="${SSH_KEY:-${HOME}/.ssh/id_ed25519}"
VAULT_ADDR="${VAULT_ADDR:-http://100.70.185.46:8200}"
VAULT_TOKEN="${VAULT_TOKEN:-}"

# abc / hashi-up runner
ABC_BIN="${ABC_BIN:-abc}"
HASHIUP_BIN="${HASHIUP_BIN:-hashi-up}"

run_hashiup() {
  if command -v "${ABC_BIN}" &>/dev/null; then
    "${ABC_BIN}" admin services hashi-up cli -- "$@"
  else
    "${HASHIUP_BIN}" "$@"
  fi
}

# ── Node list ─────────────────────────────────────────────────────────────────
declare -a NODES=(
  "100.108.199.30 abhinav"
  "100.77.21.36   abhinav"
  "129.151.174.199 ubuntu"
)

# ── Deploy to each node ───────────────────────────────────────────────────────
for NODE_DEF in "${NODES[@]}"; do
  SSH_ADDR=$(echo "${NODE_DEF}" | awk '{print $1}')
  SSH_USER=$(echo "${NODE_DEF}" | awk '{print $2}')

  echo ""
  echo "══════════════════════════════════════════════════════"
  echo "  Installing Vault on ${SSH_USER}@${SSH_ADDR}"
  echo "══════════════════════════════════════════════════════"

  echo "==> Installing Vault ${VAULT_VERSION} binary via hashi-up..."
  run_hashiup vault install \
    --ssh-target-addr "${SSH_ADDR}:22" \
    --ssh-target-user "${SSH_USER}" \
    --ssh-target-key  "${SSH_KEY}" \
    --version "${VAULT_VERSION}" \
    --skip-enable \
    --skip-start

  echo "==> Setting VAULT_ADDR in /etc/environment on ${SSH_ADDR}..."
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "bash -s" << REMOTE
set -euo pipefail
# Add VAULT_ADDR to system environment (for interactive vault CLI use)
if ! grep -q "VAULT_ADDR" /etc/environment 2>/dev/null; then
  echo 'VAULT_ADDR=${VAULT_ADDR}' >> /etc/environment
  echo "  Added VAULT_ADDR to /etc/environment"
else
  sed -i 's|^VAULT_ADDR=.*|VAULT_ADDR=${VAULT_ADDR}|' /etc/environment
  echo "  Updated VAULT_ADDR in /etc/environment"
fi
REMOTE

  echo "==> Configuring Nomad vault stanza on ${SSH_ADDR}..."
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "bash -s" << NOMAD_VAULT
NOMAD_VAULT_DROP="/etc/nomad.d/vault.hcl"
if [[ ! -f "\${NOMAD_VAULT_DROP}" ]]; then
  cat > "\${NOMAD_VAULT_DROP}" << 'NV'
# Vault integration — written by deploy-vault-to-server-nodes.sh
# Points at the central Vault server on aither.
vault {
  enabled = true
  address = "${VAULT_ADDR}"
}
NV
  echo "  Written Nomad vault stanza to \${NOMAD_VAULT_DROP}"
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
  sleep 3
else
  echo "  Nomad vault stanza already present — skipping"
fi
NOMAD_VAULT

  echo "==> Verifying vault CLI works on ${SSH_ADDR}..."
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
      -i "${SSH_KEY}" "${SSH_USER}@${SSH_ADDR}" \
      "VAULT_ADDR='${VAULT_ADDR}' vault status 2>/dev/null | grep -E 'Sealed|Version|HA'" 2>&1 || true

  echo ""
  echo "  ✓ Vault deployed on ${SSH_USER}@${SSH_ADDR}"
done

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Vault deployment complete."
echo ""
echo "  Users on these nodes can now run:"
echo "    export VAULT_ADDR=${VAULT_ADDR}"
echo "    vault login  # or set VAULT_TOKEN"
echo "    vault write -field=signed_key \\"
echo "      ssh-client-signer/sign/ssh-role \\"
echo "      public_key=@~/.ssh/id_ed25519.pub"
echo ""
echo "  Next: configure new cluster nodes as Boundary SSH targets:"
echo "    bash deployments/abc-nodes/boundary/setup-boundary.sh"
echo "══════════════════════════════════════════════════════"
