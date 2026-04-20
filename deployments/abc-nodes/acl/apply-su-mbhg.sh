#!/usr/bin/env bash
# apply-su-mbhg.sh
# Bootstrap ACL + MinIO for the two SU-MBHG research groups on abc-nodes (aither).
# Run from the abc-cluster-cli repo root, or from any directory — uses SCRIPT_DIR.
#
# Prerequisites:
#   NOMAD_ADDR   — Nomad API endpoint          (default: http://100.70.185.46:4646)
#   NOMAD_TOKEN  — Management token             (from 'nomad acl bootstrap')
#   MINIO_ADDR   — MinIO S3 API endpoint        (default: http://100.70.185.46:9000)
#   MINIO_USER   — MinIO root user              (default: minioadmin)
#   MINIO_PASS   — MinIO root password          (default: minioadmin)
#
# Usage:
#   export NOMAD_TOKEN=<bootstrap-management-token>
#   bash deployments/abc-nodes/acl/apply-su-mbhg.sh
#
# The script is idempotent — safe to re-run. Tokens are appended to tokens.env
# (sibling of this script) only on first creation; existing tokens are unchanged.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NOMAD_DIR="$(cd "${SCRIPT_DIR}/../nomad" && pwd)"

# --------------------------------------------------------------------------
# Defaults
# --------------------------------------------------------------------------
export NOMAD_ADDR="${NOMAD_ADDR:-http://100.70.185.46:4646}"
MINIO_ADDR="${MINIO_ADDR:-http://100.70.185.46:9000}"
MINIO_USER="${MINIO_USER:-minioadmin}"
MINIO_PASS="${MINIO_PASS:-minioadmin}"

for var in NOMAD_TOKEN; do
  if [[ -z "${!var:-}" ]]; then
    echo "ERROR: ${var} is not set." >&2; exit 1
  fi
done

# tokens.env lives next to this script; never commit it
TOKENS_FILE="${SCRIPT_DIR}/tokens.env"
touch "${TOKENS_FILE}"; chmod 600 "${TOKENS_FILE}"

# MinIO client binary
MCLI="${HOME}/.abc/binaries/mc"
if [[ ! -x "$MCLI" ]]; then
  echo "ERROR: MinIO client not found at ${MCLI}." >&2
  echo "  Install: curl -sL https://dl.min.io/client/mc/release/linux-amd64/mc \\" >&2
  echo "           -o ~/.abc/binaries/mc && chmod +x ~/.abc/binaries/mc" >&2
  exit 1
fi

echo "==> Target Nomad: ${NOMAD_ADDR}"
echo "==> Target MinIO: ${MINIO_ADDR}"
echo ""

# --------------------------------------------------------------------------
# Helper: Nomad API
# --------------------------------------------------------------------------
nomad_api() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sf -X "$method" \
      -H "X-Nomad-Token: ${NOMAD_TOKEN}" \
      -H "Content-Type: application/json" \
      "${NOMAD_ADDR}${path}" -d "$body"
  else
    curl -sf -X "$method" \
      -H "X-Nomad-Token: ${NOMAD_TOKEN}" \
      "${NOMAD_ADDR}${path}"
  fi
}

# --------------------------------------------------------------------------
# 1. Batch preemption
# --------------------------------------------------------------------------
echo "==> [Nomad] Enabling batch + sysbatch preemption..."
abc admin services nomad cli -- operator scheduler set-config \
  -preempt-batch-scheduler=true \
  -preempt-sysbatch-scheduler=true
echo ""

# --------------------------------------------------------------------------
# 2. Namespaces
# --------------------------------------------------------------------------
echo "==> [Nomad] Applying namespaces..."
abc admin services nomad cli -- namespace apply \
  "${SCRIPT_DIR}/namespaces/su-mbhg-bioinformatics.hcl"
abc admin services nomad cli -- namespace apply \
  "${SCRIPT_DIR}/namespaces/su-mbhg-hostgen.hcl"
echo "    su-mbhg-bioinformatics (priority=70) and su-mbhg-hostgen (priority=50)"
echo ""

# --------------------------------------------------------------------------
# 3. ACL policies
# --------------------------------------------------------------------------
echo "==> [Nomad] Applying ACL policies..."

apply_policy() {
  local name="$1" desc="$2" file="$3"
  abc admin services nomad cli -- acl policy apply -description "$desc" "$name" "$file"
  echo "    OK: $name"
}

apply_policy services-admin \
  "Services namespace admin" \
  "${SCRIPT_DIR}/policies/services-admin.hcl"

apply_policy observer \
  "Read-only observer (all namespaces)" \
  "${SCRIPT_DIR}/policies/observer.hcl"

apply_policy su-mbhg-bioinformatics-group-admin \
  "Group admin — SU-MBHG Bioinformatics" \
  "${SCRIPT_DIR}/policies/su-mbhg-bioinformatics-group-admin.hcl"

apply_policy su-mbhg-bioinformatics-submit \
  "nf-nomad submit — SU-MBHG Bioinformatics" \
  "${SCRIPT_DIR}/policies/su-mbhg-bioinformatics-submit.hcl"

apply_policy su-mbhg-bioinformatics-member \
  "Member — SU-MBHG Bioinformatics" \
  "${SCRIPT_DIR}/policies/su-mbhg-bioinformatics-member.hcl"

apply_policy su-mbhg-hostgen-group-admin \
  "Group admin — SU-MBHG Host Genetics" \
  "${SCRIPT_DIR}/policies/su-mbhg-hostgen-group-admin.hcl"

apply_policy su-mbhg-hostgen-submit \
  "nf-nomad submit — SU-MBHG Host Genetics" \
  "${SCRIPT_DIR}/policies/su-mbhg-hostgen-submit.hcl"

apply_policy su-mbhg-hostgen-member \
  "Member — SU-MBHG Host Genetics" \
  "${SCRIPT_DIR}/policies/su-mbhg-hostgen-member.hcl"
echo ""

# --------------------------------------------------------------------------
# 4. Nomad tokens  (naming: su-mbhg-<group>_<username>)
# --------------------------------------------------------------------------
echo "==> [Nomad] Creating tokens (name = MinIO username, secret = MinIO password)..."

create_nomad_token() {
  local name="$1" policy="$2" var_suffix="$3"
  # Skip if a line for this var already exists in tokens.env
  if grep -q "NOMAD_TOKEN_${var_suffix}=" "${TOKENS_FILE}" 2>/dev/null; then
    echo "    SKIP (already exists): ${name}"
    return
  fi
  local response secret
  response=$(nomad_api POST /v1/acl/token \
    "{\"Name\":\"${name}\",\"Type\":\"client\",\"Policies\":[\"${policy}\"]}")
  secret=$(python3 -c "import sys,json; print(json.load(sys.stdin)['SecretID'])" <<< "$response")
  echo "export NOMAD_TOKEN_${var_suffix}=${secret}  # ${name}" >> "${TOKENS_FILE}"
  printf "    %-44s %s\n" "${name}" "${secret}"
}

# Bioinformatics
create_nomad_token "su-mbhg-bioinformatics_submit" "su-mbhg-bioinformatics-submit"       "BIO_SUBMIT"
create_nomad_token "su-mbhg-bioinformatics_admin"  "su-mbhg-bioinformatics-group-admin"  "BIO_ADMIN"
create_nomad_token "su-mbhg-bioinformatics_alice"  "su-mbhg-bioinformatics-member"       "BIO_ALICE"

# Host Genetics
create_nomad_token "su-mbhg-hostgen_submit"        "su-mbhg-hostgen-submit"              "HG_SUBMIT"
create_nomad_token "su-mbhg-hostgen_admin"         "su-mbhg-hostgen-group-admin"         "HG_ADMIN"
create_nomad_token "su-mbhg-hostgen_bob"           "su-mbhg-hostgen-member"              "HG_BOB"

echo ""

# --------------------------------------------------------------------------
# 5. MinIO buckets + IAM policies + users
# --------------------------------------------------------------------------
echo "==> [MinIO] Setting alias..."
"$MCLI" alias set sunminio "${MINIO_ADDR}" "${MINIO_USER}" "${MINIO_PASS}" \
  --api s3v4 >/dev/null

echo "==> [MinIO] Creating buckets..."
for bucket in su-mbhg-bioinformatics su-mbhg-hostgen; do
  "$MCLI" mb --ignore-existing "sunminio/${bucket}"
  echo "" | "$MCLI" pipe "sunminio/${bucket}/shared/.keep" 2>/dev/null || true
  echo "    OK: ${bucket} (shared/ placeholder set)"
done
echo ""

echo "==> [MinIO] Applying IAM policies..."
for policy in \
  su-mbhg-bioinformatics-group-admin \
  su-mbhg-bioinformatics-member \
  su-mbhg-hostgen-group-admin \
  su-mbhg-hostgen-member \
  pipeline-service-account; do
  "$MCLI" admin policy create sunminio "$policy" \
    "${SCRIPT_DIR}/minio-policies/${policy}.json" 2>&1 \
    && echo "    OK: $policy" || echo "    SKIP (already exists): $policy"
done
echo ""

echo "==> [MinIO] Creating IAM users..."
# shellcheck source=/dev/null
source "${TOKENS_FILE}"

create_minio_user() {
  local username="$1" password="$2" policy="$3"
  "$MCLI" admin user add sunminio "$username" "$password" 2>&1
  "$MCLI" admin policy attach sunminio "$policy" --user "$username" 2>&1
  echo "    ${username} → ${policy}"
}

create_minio_user "su-mbhg-bioinformatics_submit" "${NOMAD_TOKEN_BIO_SUBMIT}" \
  "su-mbhg-bioinformatics-group-admin"
create_minio_user "su-mbhg-bioinformatics_admin"  "${NOMAD_TOKEN_BIO_ADMIN}" \
  "su-mbhg-bioinformatics-group-admin"
create_minio_user "su-mbhg-bioinformatics_alice"  "${NOMAD_TOKEN_BIO_ALICE}" \
  "su-mbhg-bioinformatics-member"
create_minio_user "su-mbhg-hostgen_submit"        "${NOMAD_TOKEN_HG_SUBMIT}" \
  "su-mbhg-hostgen-group-admin"
create_minio_user "su-mbhg-hostgen_admin"         "${NOMAD_TOKEN_HG_ADMIN}" \
  "su-mbhg-hostgen-group-admin"
create_minio_user "su-mbhg-hostgen_bob"           "${NOMAD_TOKEN_HG_BOB}" \
  "su-mbhg-hostgen-member"
echo ""

# --------------------------------------------------------------------------
# 6. Deploy abc-nodes-auth ForwardAuth service
# --------------------------------------------------------------------------
echo "==> [Nomad] Deploying abc-nodes-auth ForwardAuth service..."
abc admin services nomad cli -- job run \
  "${NOMAD_DIR}/abc-nodes-auth.nomad.hcl"
echo ""

# --------------------------------------------------------------------------
# 7. Traefik dynamic config
# --------------------------------------------------------------------------
echo "==> [Traefik] ForwardAuth config:"
echo "    Copy to the Traefik dynamic config directory on the host:"
echo "    sudo cp ${SCRIPT_DIR}/traefik/traefik-nomad-auth.yml /etc/traefik/dynamic/"
echo "    (Traefik hot-reloads — no restart needed.)"
echo ""

# --------------------------------------------------------------------------
# Summary
# --------------------------------------------------------------------------
echo "=================================================================="
echo " Done!  Tokens written to: ${TOKENS_FILE}"
echo " KEEP THAT FILE PRIVATE — treat each token as a password."
echo ""
echo " Convention: Nomad token Name  == MinIO username"
echo "             Nomad token SecretID == MinIO secret key"
echo ""
echo " Verify:"
echo "   abc admin services nomad cli -- namespace list"
echo "   abc admin services nomad cli -- acl policy list"
echo "   curl -si -H \"X-Nomad-Token: \${NOMAD_TOKEN_BIO_ALICE}\" \\"
echo "        ${NOMAD_ADDR%:*}:9191/auth"
echo "=================================================================="
