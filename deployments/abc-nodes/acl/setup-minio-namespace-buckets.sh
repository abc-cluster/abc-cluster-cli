#!/usr/bin/env bash
# setup-minio-namespace-buckets.sh
#
# Bootstraps MinIO buckets and IAM policies for research-group namespaces.
# - One bucket per namespace (bucket name == namespace)
# - Member layout: users/<user>/* (private), shared/<user>/* (collaboration); list only under users/ and shared/
# - Group admin policy: full access to all objects in that namespace bucket
# - Cluster admin policy: full access to all configured namespace buckets
#
# Idempotent:
# - Buckets are created with --ignore-existing
# - Policy files are (re)generated under minio-policies/generated/
# - Policies are updated by remove+create when they already exist
# - Users are created if missing, then policies are attached
#
# Prereqs:
#   ~/.abc/binaries/mc exists and is executable
#   MINIO_ADDR / MINIO_USER / MINIO_PASS point to your MinIO root credentials
#
# Usage:
#   bash deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh
#
# Optional env overrides:
#   MINIO_ALIAS=sunminio
#   MINIO_ADDR=http://100.70.185.46:9000
#   MINIO_USER=minioadmin
#   MINIO_PASS=minioadmin
#   CLUSTER_ADMIN_USERS=cluster_services_admin,abc-cluster-admin,cluster-admin-gvds,cluster-admin-abhi
#   CLUSTER_ADMIN_IAM_USER=abc-cluster-admin
#   CLUSTER_ADMIN_IAM_PASS=<strong-password>
#   USER_PASSWORD_PREFIX=minio-
#   LOCAL_CREDENTIALS_FILE=deployments/abc-nodes/acl/minio-credentials.env
#   SYNC_NOMAD_VARS=1
#   NOMAD_IAM_VAR_PREFIX=nomad/jobs/abc-nodes-minio-iam
#   NOMAD_IAM_NAMESPACE=abc-services
#   SYNC_VAULT=0
#   VAULT_ADDR=http://100.70.185.46:8200
#   VAULT_KV_MOUNT=secret
#   VAULT_IAM_PREFIX=abc-nodes/minio-iam
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POLICY_OUT_DIR="${SCRIPT_DIR}/minio-policies/generated"
mkdir -p "${POLICY_OUT_DIR}"

MCLI="${HOME}/.abc/binaries/mc"
if [[ ! -x "${MCLI}" ]]; then
  echo "ERROR: mc not found at ${MCLI}" >&2
  exit 1
fi

MINIO_ALIAS="${MINIO_ALIAS:-sunminio}"
MINIO_ADDR="${MINIO_ADDR:-http://100.70.185.46:9000}"
MINIO_USER="${MINIO_USER:-minioadmin}"
MINIO_PASS="${MINIO_PASS:-minioadmin}"
USER_PASSWORD_PREFIX="${USER_PASSWORD_PREFIX:-minio-}"
CLUSTER_ADMIN_USERS="${CLUSTER_ADMIN_USERS:-cluster_services_admin,abc-cluster-admin,cluster-admin-gvds,cluster-admin-abhi}"
CLUSTER_ADMIN_IAM_USER="${CLUSTER_ADMIN_IAM_USER:-}"
CLUSTER_ADMIN_IAM_PASS="${CLUSTER_ADMIN_IAM_PASS:-}"
LOCAL_CREDENTIALS_FILE="${LOCAL_CREDENTIALS_FILE:-${SCRIPT_DIR}/minio-credentials.env}"
SYNC_NOMAD_VARS="${SYNC_NOMAD_VARS:-1}"
NOMAD_IAM_VAR_PREFIX="${NOMAD_IAM_VAR_PREFIX:-nomad/jobs/abc-nodes-minio-iam}"
NOMAD_IAM_NAMESPACE="${NOMAD_IAM_NAMESPACE:-abc-services}"
SYNC_VAULT="${SYNC_VAULT:-0}"
VAULT_ADDR="${VAULT_ADDR:-http://100.70.185.46:8200}"
VAULT_KV_MOUNT="${VAULT_KV_MOUNT:-secret}"
VAULT_IAM_PREFIX="${VAULT_IAM_PREFIX:-abc-nodes/minio-iam}"

declare -A NS_USERS
NS_USERS["su-mbhg-bioinformatics"]="kim"
NS_USERS["su-mbhg-hostgen"]="yentl,devon,dayna"
NS_USERS["su-mbhg-animaltb"]="stacey"
NS_USERS["su-psy-neuropsychiatry"]="lauren"
NS_USERS["su-sdsct-ceri"]="tj,eduan"
NS_USERS["su-mbhg-tbgenomics"]="hanno"

CREDENTIALS_TMP="$(mktemp)"
cleanup() {
  rm -f "${CREDENTIALS_TMP}"
}
trap cleanup EXIT

echo "==> MinIO alias: ${MINIO_ALIAS} (${MINIO_ADDR})"
"${MCLI}" alias set "${MINIO_ALIAS}" "${MINIO_ADDR}" "${MINIO_USER}" "${MINIO_PASS}" --api s3v4 >/dev/null

policy_upsert() {
  local policy_name="$1"
  local policy_file="$2"
  if "${MCLI}" admin policy info "${MINIO_ALIAS}" "${policy_name}" >/dev/null 2>&1; then
    "${MCLI}" admin policy remove "${MINIO_ALIAS}" "${policy_name}" >/dev/null 2>&1 || true
  fi
  "${MCLI}" admin policy create "${MINIO_ALIAS}" "${policy_name}" "${policy_file}" >/dev/null
  echo "    policy: ${policy_name}"
}

ensure_user() {
  local username="$1"
  local password="$2"
  if ! "${MCLI}" admin user info "${MINIO_ALIAS}" "${username}" >/dev/null 2>&1; then
    "${MCLI}" admin user add "${MINIO_ALIAS}" "${username}" "${password}" >/dev/null
    echo "      created user: ${username}"
  fi
}

record_credential() {
  local principal="$1"
  local secret="$2"
  local role="$3"
  local scope="$4"
  local bucket="$5"
  printf '%s|%s|%s|%s|%s\n' "${principal}" "${secret}" "${role}" "${scope}" "${bucket}" >> "${CREDENTIALS_TMP}"
}

attach_policy_user() {
  local policy_name="$1"
  local username="$2"
  if "${MCLI}" admin policy attach "${MINIO_ALIAS}" "${policy_name}" --user "${username}" >/dev/null 2>&1; then
    echo "      attached ${policy_name} -> ${username}"
  else
    echo "      skip attach (not allowed or user missing): ${username}"
  fi
}

build_group_admin_policy() {
  local bucket="$1"
  local out="$2"
  cat > "${out}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": ["arn:aws:s3:::${bucket}"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": ["arn:aws:s3:::${bucket}/*"]
    }
  ]
}
EOF
}

build_user_policy() {
  local bucket="$1"
  local username="$2"
  local out="$3"
  local private_prefix="users/${username}/"
  local shared_user_prefix="shared/${username}/"
  local shared_prefix="shared/"
  local users_prefix="users/"
  cat > "${out}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetBucketLocation"],
      "Resource": ["arn:aws:s3:::${bucket}"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": ["arn:aws:s3:::${bucket}"],
      "Condition": {
        "StringLike": {
          "s3:prefix": [
            "${users_prefix}",
            "${users_prefix}*",
            "${shared_prefix}",
            "${shared_prefix}*",
            "${private_prefix}",
            "${private_prefix}*",
            "${shared_user_prefix}",
            "${shared_user_prefix}*"
          ]
        }
      }
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject"],
      "Resource": ["arn:aws:s3:::${bucket}/${shared_prefix}*"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": [
        "arn:aws:s3:::${bucket}/${private_prefix}*",
        "arn:aws:s3:::${bucket}/${shared_user_prefix}*"
      ]
    }
  ]
}
EOF
}

declare -a BUCKETS=()

echo "==> Creating buckets, folder placeholders, and namespace policies..."
for ns in "${!NS_USERS[@]}"; do
  BUCKETS+=("${ns}")
  "${MCLI}" mb --ignore-existing "${MINIO_ALIAS}/${ns}" >/dev/null
  echo "  bucket: ${ns}"

  # Common placeholders
  echo "" | "${MCLI}" pipe "${MINIO_ALIAS}/${ns}/shared/.keep" >/dev/null 2>&1 || true

  # Group admin policy + admin user
  group_policy="ns-${ns}-group-admin"
  group_policy_file="${POLICY_OUT_DIR}/${group_policy}.json"
  build_group_admin_policy "${ns}" "${group_policy_file}"
  policy_upsert "${group_policy}" "${group_policy_file}"

  group_admin_user="${ns}_admin"
  group_admin_pass="${USER_PASSWORD_PREFIX}${group_admin_user}"
  ensure_user "${group_admin_user}" "${group_admin_pass}"
  attach_policy_user "${group_policy}" "${group_admin_user}"
  record_credential "${group_admin_user}" "${group_admin_pass}" "group-admin" "bucket-full" "${ns}"

  # Per-user policies
  IFS=',' read -r -a users <<< "${NS_USERS[$ns]}"
  for u in "${users[@]}"; do
    echo "" | "${MCLI}" pipe "${MINIO_ALIAS}/${ns}/users/${u}/.keep" >/dev/null 2>&1 || true
    echo "" | "${MCLI}" pipe "${MINIO_ALIAS}/${ns}/shared/${u}/.keep" >/dev/null 2>&1 || true
    user_policy="ns-${ns}-user-${u}"
    user_policy_file="${POLICY_OUT_DIR}/${user_policy}.json"
    build_user_policy "${ns}" "${u}" "${user_policy_file}"
    policy_upsert "${user_policy}" "${user_policy_file}"

    ns_user="${ns}_${u}"
    ns_user_pass="${USER_PASSWORD_PREFIX}${ns_user}"
    ensure_user "${ns_user}" "${ns_user_pass}"
    attach_policy_user "${user_policy}" "${ns_user}"
    record_credential "${ns_user}" "${ns_user_pass}" "user" "users/${u}+shared/${u}" "${ns}"
  done
done

echo "==> Creating cluster-admin all-data policy..."
cluster_policy="cluster-admin-all-namespace-data"
cluster_policy_file="${POLICY_OUT_DIR}/${cluster_policy}.json"
{
  echo '{'
  echo '  "Version": "2012-10-17",'
  echo '  "Statement": ['
  echo '    {'
  echo '      "Effect": "Allow",'
  echo '      "Action": ["s3:ListBucket", "s3:GetBucketLocation"],'
  echo '      "Resource": ['
  for i in "${!BUCKETS[@]}"; do
    comma=","
    [[ "${i}" == "$((${#BUCKETS[@]}-1))" ]] && comma=""
    printf '        "arn:aws:s3:::%s"%s\n' "${BUCKETS[$i]}" "${comma}"
  done
  echo '      ]'
  echo '    },'
  echo '    {'
  echo '      "Effect": "Allow",'
  echo '      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],'
  echo '      "Resource": ['
  for i in "${!BUCKETS[@]}"; do
    comma=","
    [[ "${i}" == "$((${#BUCKETS[@]}-1))" ]] && comma=""
    printf '        "arn:aws:s3:::%s/*"%s\n' "${BUCKETS[$i]}" "${comma}"
  done
  echo '      ]'
  echo '    }'
  echo '  ]'
  echo '}'
} > "${cluster_policy_file}"

policy_upsert "${cluster_policy}" "${cluster_policy_file}"

echo "==> Attaching cluster-admin policy to configured admin users..."
# Always include the MinIO root/admin account used for bootstrap so there is
# at least one guaranteed principal with full access across all namespace buckets.
attach_policy_user "${cluster_policy}" "${MINIO_USER}"
IFS=',' read -r -a admin_users <<< "${CLUSTER_ADMIN_USERS}"
for admin_u in "${admin_users[@]}"; do
  [[ "${admin_u}" == "${MINIO_USER}" ]] && continue
  # Attach only if user already exists to avoid unexpected credential creation.
  if "${MCLI}" admin user info "${MINIO_ALIAS}" "${admin_u}" >/dev/null 2>&1; then
    attach_policy_user "${cluster_policy}" "${admin_u}"
  else
    echo "      skip (user missing): ${admin_u}"
  fi
done

if [[ -n "${CLUSTER_ADMIN_IAM_USER}" || -n "${CLUSTER_ADMIN_IAM_PASS}" ]]; then
  echo "==> Ensuring dedicated cluster-admin IAM user..."
  if [[ -z "${CLUSTER_ADMIN_IAM_USER}" || -z "${CLUSTER_ADMIN_IAM_PASS}" ]]; then
    echo "      skip: set both CLUSTER_ADMIN_IAM_USER and CLUSTER_ADMIN_IAM_PASS"
  else
    ensure_user "${CLUSTER_ADMIN_IAM_USER}" "${CLUSTER_ADMIN_IAM_PASS}"
    attach_policy_user "${cluster_policy}" "${CLUSTER_ADMIN_IAM_USER}"
    record_credential "${CLUSTER_ADMIN_IAM_USER}" "${CLUSTER_ADMIN_IAM_PASS}" "cluster-admin" "all-namespace-buckets" "*"
  fi
fi

if [[ "${SYNC_NOMAD_VARS}" == "1" ]]; then
  echo "==> Syncing IAM credentials to Nomad variables (namespace=${NOMAD_IAM_NAMESPACE})..."
  while IFS='|' read -r principal secret role scope bucket; do
    var_path="${NOMAD_IAM_VAR_PREFIX}/${principal}"
    abc admin services nomad cli -- var put -namespace "${NOMAD_IAM_NAMESPACE}" -force \
      "${var_path}" \
      access_key="${principal}" \
      secret_key="${secret}" \
      role="${role}" \
      scope="${scope}" \
      bucket="${bucket}" >/dev/null
    echo "      stored var: ${var_path}"
  done < "${CREDENTIALS_TMP}"
fi

if [[ "${SYNC_VAULT}" == "1" ]]; then
  echo "==> Syncing IAM credentials to Vault KV v2..."
  : "${VAULT_TOKEN:?Must set VAULT_TOKEN when SYNC_VAULT=1}"
  while IFS='|' read -r principal secret role scope bucket; do
    vault_path="${VAULT_IAM_PREFIX}/${principal}"
    payload="$(python3 -c 'import json,sys; p,s,r,sc,b=sys.argv[1:]; print(json.dumps({"data":{"access_key":p,"secret_key":s,"role":r,"scope":sc,"bucket":b}}))' \
      "${principal}" "${secret}" "${role}" "${scope}" "${bucket}")"
    curl -sf -X POST \
      -H "X-Vault-Token: ${VAULT_TOKEN}" \
      -H "Content-Type: application/json" \
      "${VAULT_ADDR}/v1/${VAULT_KV_MOUNT}/data/${vault_path}" \
      -d "${payload}" >/dev/null
    echo "      stored vault: ${VAULT_KV_MOUNT}/${vault_path}"
  done < "${CREDENTIALS_TMP}"
fi

echo "==> Writing local IAM credential snapshot..."
touch "${LOCAL_CREDENTIALS_FILE}"
chmod 600 "${LOCAL_CREDENTIALS_FILE}"
{
  echo "# MinIO IAM credentials generated by setup-minio-namespace-buckets.sh"
  echo "# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "# KEEP SECRET. Never commit."
  while IFS='|' read -r principal secret role scope bucket; do
    upper="$(printf '%s' "${principal}" | tr '[:lower:]-' '[:upper:]_')"
    echo "export MINIO_IAM_USER_${upper}=${principal}"
    echo "export MINIO_IAM_SECRET_${upper}=${secret}"
    echo "export MINIO_IAM_ROLE_${upper}=${role}"
    echo "export MINIO_IAM_SCOPE_${upper}=${scope}"
    echo "export MINIO_IAM_BUCKET_${upper}=${bucket}"
    echo ""
  done < "${CREDENTIALS_TMP}"
} > "${LOCAL_CREDENTIALS_FILE}"

echo ""
echo "Done."
echo "Generated policies: ${POLICY_OUT_DIR}"
echo "Local credentials: ${LOCAL_CREDENTIALS_FILE}"
echo "Namespace buckets: ${BUCKETS[*]}"
echo "Cluster-admin policy: ${cluster_policy}"
