#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
NOMAD_DIR="${REPO_ROOT}/deployments/abc-nodes/nomad"

echo "==> Migration: services/applications -> abc-services/abc-applications"

apply_ns() {
  local file="$1"
  echo "    namespace apply: $(basename "${file}")"
  abc admin services nomad cli -- namespace apply "${file}"
}

apply_policy() {
  local name="$1"
  local file="$2"
  echo "    policy apply: ${name}"
  abc admin services nomad cli -- acl policy apply -description "${name}" "${name}" "${file}"
}

ensure_nomad_token() {
  local name="$1"
  local policy="$2"
  local existing
  existing="$(abc admin services nomad cli -- acl token list -json | python3 -c 'import json,sys; n=sys.argv[1]; arr=json.load(sys.stdin); print(next((t["AccessorID"] for t in arr if t.get("Name")==n),""))' "${name}")"
  if [[ -n "${existing}" ]]; then
    echo "      token exists: ${name}"
    return 0
  fi
  local created secret
  created="$(abc admin services nomad cli -- acl token create -name "${name}" -policy "${policy}" -json)"
  secret="$(printf '%s\n' "${created}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["SecretID"])')"
  abc admin services nomad cli -- var put -namespace abc-services -force \
    "nomad/jobs/abc-nodes-minio-iam/${name}" \
    access_key="${name}" \
    secret_key="${secret}" \
    role="cluster-admin" \
    bucket="*" >/dev/null
  echo "      token created: ${name}"
}

copy_var_path() {
  local src_ns="$1"
  local dst_ns="$2"
  local path="$3"
  local json
  if ! json="$(abc admin services nomad cli -- var get -namespace "${src_ns}" "${path}" 2>/dev/null)"; then
    echo "      skip var (missing): ${src_ns}/${path}"
    return 0
  fi
  python3 -c 'import json, subprocess, sys
dst_ns, path, payload = sys.argv[1], sys.argv[2], sys.argv[3]
obj = json.loads(payload)
items = obj.get("Items", {})
if not items:
    sys.exit(0)
cmd = ["abc", "admin", "services", "nomad", "cli", "--", "var", "put", "-namespace", dst_ns, "-force", path]
for k, v in items.items():
    cmd.append(f"{k}={v}")
subprocess.run(cmd, check=True)
' "${dst_ns}" "${path}" "${json}"
  echo "      copied var: ${path} (${src_ns} -> ${dst_ns})"
}

echo "==> Applying base namespaces..."
apply_ns "${SCRIPT_DIR}/namespaces/abc-services.hcl"
apply_ns "${SCRIPT_DIR}/namespaces/abc-applications.hcl"

echo "==> Applying research namespaces..."
for ns_file in \
  "${SCRIPT_DIR}/namespaces/su-mbhg-bioinformatics.hcl" \
  "${SCRIPT_DIR}/namespaces/su-mbhg-hostgen.hcl"; do
  apply_ns "${ns_file}"
done

echo "==> Applying base policies..."
apply_policy "services-admin" "${SCRIPT_DIR}/policies/services-admin.hcl"
apply_policy "applications-admin" "${SCRIPT_DIR}/policies/applications-admin.hcl"

echo "==> Applying research-group admin policies..."
for p in \
  "su-mbhg-bioinformatics-group-admin" \
  "su-mbhg-hostgen-group-admin"; do
  apply_policy "${p}" "${SCRIPT_DIR}/policies/${p}.hcl"
done

echo "==> Copying Nomad variables to abc-services..."
for path in \
  nomad/jobs/abc-nodes-minio \
  nomad/jobs/abc-nodes-loki \
  nomad/jobs/abc-nodes-ntfy \
  nomad/jobs/abc-nodes-tusd \
  nomad/jobs/abc-nodes-grafana \
  nomad/jobs/abc-nodes-job-notifier \
  nomad/jobs/abc-nodes-supabase \
  nomad/jobs/abc-nodes-wave \
  nomad/jobs/abc-nodes-minio-iam; do
  copy_var_path "services" "abc-services" "${path}" || true
done

echo "==> Bootstrapping MinIO research buckets/users/policies..."
creds="$(abc admin services nomad cli -- var get -namespace abc-services nomad/jobs/abc-nodes-minio 2>/dev/null || true)"
if [[ -z "${creds}" ]]; then
  creds="$(abc admin services nomad cli -- var get -namespace services nomad/jobs/abc-nodes-minio)"
fi
minio_user="$(printf '%s\n' "${creds}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["Items"]["minio_root_user"])')"
minio_pass="$(printf '%s\n' "${creds}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["Items"]["minio_root_password"])')"

NOMAD_IAM_NAMESPACE="abc-services" \
MINIO_USER="${minio_user}" \
MINIO_PASS="${minio_pass}" \
CLUSTER_ADMIN_USERS="cluster_services_admin,abc-cluster-admin,cluster-admin-gvds,cluster-admin-abhi" \
CLUSTER_ADMIN_IAM_USER="abc-cluster-admin" \
CLUSTER_ADMIN_IAM_PASS="${minio_pass}" \
bash "${SCRIPT_DIR}/setup-minio-namespace-buckets.sh"

echo "==> Ensuring cluster-admin Nomad tokens..."
ensure_nomad_token "cluster_services_admin" "services-admin"
ensure_nomad_token "cluster-admin-gvds" "admin"
ensure_nomad_token "cluster-admin-abhi" "admin"

echo "==> Migrating service job specs into abc-services namespace..."
for file in \
  minio.nomad.hcl loki.nomad.hcl ntfy.nomad.hcl grafana.nomad.hcl job-notifier.nomad.hcl \
  traefik.nomad.hcl abc-nodes-auth.nomad.hcl prometheus.nomad.hcl alloy.nomad.hcl \
  vault.nomad.hcl tusd.nomad.hcl uppy.nomad.hcl redis.nomad.hcl rustfs.nomad.hcl \
  docker-registry.nomad.hcl wave.nomad.hcl supabase.nomad.hcl postgres.nomad.hcl; do
    src="${NOMAD_DIR}/${file}"
  if [[ -f "${src}" ]]; then
    tmp="$(mktemp)"
    perl -pe 's/namespace\s*=\s*"services"/namespace = "abc-services"/g' "${src}" > "${tmp}"
    abc admin services nomad cli -- job run "${tmp}" >/dev/null
    rm -f "${tmp}"
    echo "      deployed: ${file} -> abc-services"
  fi
done

echo ""
echo "Migration applied."
echo "Next optional cleanup: stop old jobs from namespace 'services' once abc-services jobs are verified healthy."
