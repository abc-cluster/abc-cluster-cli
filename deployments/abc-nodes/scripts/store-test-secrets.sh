#!/usr/bin/env bash
# store-test-secrets.sh
#
# Populates Nomad Variable paths for each abc-nodes test job.
#
# Each test job can only read Nomad Variables at its own path:
#   nomad/jobs/<job-id>  (Nomad ACL policy: "nomad/jobs/*")
#
# This script stores the required credentials at each job's own path:
#   nomad/jobs/abc-nodes-test-storage-minio      minio_access_key, minio_secret_key
#   nomad/jobs/abc-nodes-test-upload-tusd        minio_access_key, minio_secret_key, nomad_token
#   nomad/jobs/abc-nodes-test-auth-forwardauth   nomad_token
#   nomad/jobs/abc-nodes-test-vault              vault_token
#   nomad/jobs/abc-nodes-test-observability      grafana_admin_password
#
# Prerequisites:
#   NOMAD_TOKEN — management token (must be able to read/write all service Variables)
#
# Usage:
#   export NOMAD_TOKEN=<management-token>
#   bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Optional overrides (all default to auto-discovery or empty):
#   VAULT_TEST_TOKEN     — Vault root or rw token (from experimental/acl or legacy acl/vault-keys.env)
#   NOMAD_TEST_TOKEN     — Nomad ACL token for ForwardAuth tests (defaults to NOMAD_TOKEN)

set -euo pipefail

: "${NOMAD_TOKEN:?Must set NOMAD_TOKEN}"

NOMAD_CMD="abc admin services nomad cli --"

echo "==> Populating Nomad Variable paths for abc-nodes test jobs..."
echo "    NOMAD_TOKEN: ${NOMAD_TOKEN:0:8}..."
echo ""

# ─── MinIO credentials ────────────────────────────────────────────────────────
echo "==> [1/4] Reading MinIO credentials from nomad/jobs/abc-nodes-minio..."
minio_json=$(${NOMAD_CMD} var get -namespace services -out json \
  nomad/jobs/abc-nodes-minio 2>/dev/null)

if [ -z "$minio_json" ]; then
  echo "    ERROR: nomad/jobs/abc-nodes-minio not found."
  echo "    Run: bash deployments/abc-nodes/scripts/store-cluster-secrets.sh first."
  exit 1
fi

MINIO_ACCESS_KEY=$(echo "$minio_json" | jq -r '.Items.minio_root_user // .Items.minio_access_key // empty')
MINIO_SECRET_KEY=$(echo "$minio_json" | jq -r '.Items.minio_root_password // .Items.minio_secret_key // empty')
if [ -z "$MINIO_ACCESS_KEY" ] || [ -z "$MINIO_SECRET_KEY" ]; then
  echo "    ERROR: could not read MinIO credentials from nomad/jobs/abc-nodes-minio."
  echo "    Expected keys: minio_root_user/minio_root_password or minio_access_key/minio_secret_key."
  exit 1
fi
printf "    access_key: %s\n" "$MINIO_ACCESS_KEY"
printf "    secret_key: %s...\n" "${MINIO_SECRET_KEY:0:6}"

# ─── Vault token ──────────────────────────────────────────────────────────────
echo ""
echo "==> [2/4] Vault token..."
if [ -z "${VAULT_TEST_TOKEN:-}" ]; then
  _vk=""
  if [ -f "deployments/abc-nodes/experimental/acl/vault-keys.env" ]; then
    _vk="deployments/abc-nodes/experimental/acl/vault-keys.env"
  elif [ -f "deployments/abc-nodes/acl/vault-keys.env" ]; then
    _vk="deployments/abc-nodes/acl/vault-keys.env"
  fi
  if [ -n "${_vk}" ]; then
    # shellcheck disable=SC1091
    VAULT_TEST_TOKEN=$(grep "VAULT_ROOT_TOKEN=" "${_vk}" \
      | cut -d= -f2- | tr -d '"' | tr -d "'" || true)
    [ -n "$VAULT_TEST_TOKEN" ] \
      && printf "    loaded from %s\n" "${_vk}" \
      || printf "    %s exists but VAULT_ROOT_TOKEN not found — leaving empty\n" "${_vk}"
  else
    VAULT_TEST_TOKEN=""
    printf "    vault-keys.env not found (experimental/acl or acl/) — vault tests will be skipped\n"
    printf "    Set VAULT_TEST_TOKEN=<token> to enable them.\n"
  fi
else
  printf "    using VAULT_TEST_TOKEN from environment\n"
fi
printf "    token: %s\n" "${VAULT_TEST_TOKEN:0:8}${VAULT_TEST_TOKEN:+...}"

# ─── Nomad ACL token ──────────────────────────────────────────────────────────
echo ""
echo "==> [3/4] Nomad ACL token for ForwardAuth tests..."
NOMAD_TEST_TOKEN="${NOMAD_TEST_TOKEN:-${NOMAD_TOKEN}}"
printf "    token: %s...\n" "${NOMAD_TEST_TOKEN:0:8}"

# ─── Grafana admin password ───────────────────────────────────────────────────
echo ""
echo "==> [4/4] Grafana admin password from nomad/jobs/abc-nodes-grafana..."
grafana_json=$(${NOMAD_CMD} var get -namespace services -out json \
  nomad/jobs/abc-nodes-grafana 2>/dev/null || true)

if [ -n "$grafana_json" ]; then
  GRAFANA_ADMIN_PASSWORD=$(echo "$grafana_json" | jq -r '.Items.admin_password // empty')
  if [ -n "$GRAFANA_ADMIN_PASSWORD" ]; then
    printf "    loaded from nomad/jobs/abc-nodes-grafana\n"
    printf "    password: %s...\n" "${GRAFANA_ADMIN_PASSWORD:0:6}"
  else
    GRAFANA_ADMIN_PASSWORD="admin"
    printf "    key admin_password not found — defaulting to 'admin'\n"
  fi
else
  GRAFANA_ADMIN_PASSWORD="admin"
  printf "    nomad/jobs/abc-nodes-grafana not found — defaulting to 'admin'\n"
fi

# ─── Store into each test job's own variable path ─────────────────────────────
echo ""
echo "==> Storing secrets into each test job's own Nomad Variable path..."

echo "    → nomad/jobs/abc-nodes-test-storage-minio"
${NOMAD_CMD} var put -namespace services -force \
  nomad/jobs/abc-nodes-test-storage-minio \
  minio_access_key="${MINIO_ACCESS_KEY}" \
  minio_secret_key="${MINIO_SECRET_KEY}"

echo "    → nomad/jobs/abc-nodes-test-upload-tusd"
${NOMAD_CMD} var put -namespace services -force \
  nomad/jobs/abc-nodes-test-upload-tusd \
  nomad_token="${NOMAD_TEST_TOKEN}" \
  minio_access_key="${MINIO_ACCESS_KEY}" \
  minio_secret_key="${MINIO_SECRET_KEY}"

echo "    → nomad/jobs/abc-nodes-test-auth-forwardauth"
${NOMAD_CMD} var put -namespace services -force \
  nomad/jobs/abc-nodes-test-auth-forwardauth \
  nomad_token="${NOMAD_TEST_TOKEN}"

echo "    → nomad/jobs/abc-nodes-test-vault"
if [ -n "${VAULT_TEST_TOKEN:-}" ]; then
  ${NOMAD_CMD} var put -namespace services -force \
    nomad/jobs/abc-nodes-test-vault \
    vault_token="${VAULT_TEST_TOKEN}"
else
  # Store a placeholder so the variable path exists and the template renders;
  # the test script will skip KV tests if the token is empty.
  ${NOMAD_CMD} var put -namespace services -force \
    nomad/jobs/abc-nodes-test-vault \
    vault_token="PLACEHOLDER_NOT_SET"
  printf "    WARNING: vault_token is empty — KV round-trip tests will be skipped\n"
  printf "    Set VAULT_TEST_TOKEN=<token> and re-run to enable them.\n"
fi

echo "    → nomad/jobs/abc-nodes-test-observability"
${NOMAD_CMD} var put -namespace services -force \
  nomad/jobs/abc-nodes-test-observability \
  grafana_admin_password="${GRAFANA_ADMIN_PASSWORD}"

echo ""
echo "    Done — all 5 variable paths populated."
echo ""
echo "=================================================================="
echo " Test secrets stored. You can now run the test suite:"
echo ""
echo "   bash deployments/abc-nodes/scripts/run-tests.sh"
echo ""
echo " Or run individual tests:"
echo "   abc admin services nomad cli -- job run -detach \\"
echo "     deployments/abc-nodes/nomad/tests/connectivity.nomad.hcl"
echo "=================================================================="
