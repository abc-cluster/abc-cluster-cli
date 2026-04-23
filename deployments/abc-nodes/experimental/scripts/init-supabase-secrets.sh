#!/usr/bin/env bash
# init-supabase-secrets.sh
#
# Generate fresh Supabase JWT tokens + store all required Nomad Variables
# for the abc-nodes-supabase job.
#
# Prerequisites:
#   NOMAD_TOKEN — Nomad management token (services namespace)
#   openssl     — for random key generation and HMAC signing
#
# Usage:
#   export NOMAD_TOKEN=<token>
#   bash deployments/abc-nodes/experimental/scripts/init-supabase-secrets.sh
#
# Idempotent: re-running regenerates and overwrites existing Variables.
# To rotate credentials, just re-run this script + redeploy the job.

set -euo pipefail

: "${NOMAD_TOKEN:?Must set NOMAD_TOKEN}"

# ─────────────────────────────────────────────────────────────────────────────
# JWT generation helpers
# ─────────────────────────────────────────────────────────────────────────────
b64url() {
  # base64url-encode stdin (no padding)
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

make_jwt() {
  # make_jwt <role> <jwt_secret>
  local role="$1" secret="$2"
  local now exp
  now=$(date +%s)
  exp=$((now + 315360000))  # ~10 years

  local header payload
  header=$(printf '{"alg":"HS256","typ":"JWT"}' | b64url)
  payload=$(printf '{"iss":"supabase","ref":"abc-nodes","role":"%s","iat":%d,"exp":%d}' \
    "$role" "$now" "$exp" | b64url)

  local sig
  sig=$(printf '%s.%s' "$header" "$payload" \
    | openssl dgst -sha256 -hmac "$secret" -binary | b64url)

  printf '%s.%s.%s' "$header" "$payload" "$sig"
}

# ─────────────────────────────────────────────────────────────────────────────
# Generate secrets
# ─────────────────────────────────────────────────────────────────────────────
echo "==> Generating Supabase secrets..."

POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 28)}"
WAVE_DB_PASSWORD="${WAVE_DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 28)}"
JWT_SECRET="${JWT_SECRET:-$(openssl rand -base64 40 | tr -d '/+=' | head -c 48)}"

echo "    Generating anon JWT..."
ANON_KEY=$(make_jwt "anon" "$JWT_SECRET")

echo "    Generating service_role JWT..."
SERVICE_ROLE_KEY=$(make_jwt "service_role" "$JWT_SECRET")

echo ""
echo "    postgres_password : ${POSTGRES_PASSWORD}"
echo "    wave_db_password  : ${WAVE_DB_PASSWORD}"
echo "    jwt_secret        : ${JWT_SECRET}"
echo "    anon_key          : ${ANON_KEY:0:40}..."
echo "    service_role_key  : ${SERVICE_ROLE_KEY:0:40}..."
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Store in Nomad Variables (namespace: services)
# ─────────────────────────────────────────────────────────────────────────────
echo "==> Storing in Nomad Variable: nomad/jobs/abc-nodes-supabase ..."
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-supabase \
  postgres_password="${POSTGRES_PASSWORD}" \
  wave_db_password="${WAVE_DB_PASSWORD}" \
  jwt_secret="${JWT_SECRET}" \
  anon_key="${ANON_KEY}" \
  service_role_key="${SERVICE_ROLE_KEY}"

echo "    Done."
echo ""

# Also store wave_db_password in wave's own Variable so wave.nomad.hcl can read it
echo "==> Storing wave_db_password in nomad/jobs/abc-nodes-wave ..."
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-wave \
  wave_db_password="${WAVE_DB_PASSWORD}"
echo "    Done."
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Save to local file for reference (never commit this)
# ─────────────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KEYS_FILE="${SCRIPT_DIR}/../acl/supabase-secrets.env"
touch "${KEYS_FILE}"; chmod 600 "${KEYS_FILE}"

cat > "${KEYS_FILE}" <<ENV
# Supabase secrets — KEEP SECRET, do not commit
# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)
export SUPABASE_POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
export SUPABASE_WAVE_DB_PASSWORD=${WAVE_DB_PASSWORD}
export SUPABASE_JWT_SECRET=${JWT_SECRET}
export SUPABASE_ANON_KEY=${ANON_KEY}
export SUPABASE_SERVICE_ROLE_KEY=${SERVICE_ROLE_KEY}
ENV

echo "=== Summary =========================================================="
echo ""
echo "  Secrets stored in Nomad Variables AND saved to:"
echo "  ${KEYS_FILE}  ← KEEP PRIVATE, never commit"
echo ""
echo "  Next steps:"
echo "    1. Stop the old postgres job (if running):"
echo "       abc admin services nomad cli -- job stop abc-nodes-postgres"
echo ""
echo "    2. Deploy Supabase:"
echo "       abc admin services nomad cli -- job run \\"
echo "         deployments/abc-nodes/experimental/nomad/supabase.nomad.hcl"
echo ""
echo "    3. Deploy Wave (uses updated DB credentials):"
echo "       abc admin services nomad cli -- job run \\"
echo "         deployments/abc-nodes/experimental/nomad/wave.nomad.hcl"
echo ""
echo "    4. Access Supabase Studio:"
echo "       open http://supabase-studio.aither"
echo "       # or: curl http://100.70.185.46:8000"
echo ""
echo "    5. Verify Wave DB connection:"
echo "       psql postgresql://wave:${WAVE_DB_PASSWORD}@100.70.185.46:5432/wave -c '\l'"
echo "======================================================================"
