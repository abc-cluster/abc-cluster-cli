#!/usr/bin/env bash
# store-cluster-secrets.sh
#
# One-shot bootstrap: store all required Nomad Variables for abc-nodes services.
# Run this BEFORE deploying or redeploying jobs after a fresh cluster setup
# or credential rotation.
#
# Services covered:
#   MinIO           nomad/jobs/abc-nodes-minio
#   Grafana         nomad/jobs/abc-nodes-grafana
#   Job Notifier    nomad/jobs/abc-nodes-job-notifier
#
# Supabase secrets are handled separately (init-supabase-secrets.sh generates
# JWT tokens that cannot be derived here):
#   bash deployments/abc-nodes/scripts/init-supabase-secrets.sh
#
# Vault init is also separate:
#   bash deployments/abc-nodes/scripts/init-vault.sh
#
# Prerequisites:
#   NOMAD_TOKEN — Nomad management token (services namespace access)
#
# Usage:
#   export NOMAD_TOKEN=<token>
#   bash deployments/abc-nodes/scripts/store-cluster-secrets.sh
#
# Environment variable overrides (all optional — defaults generated if absent):
#   MINIO_ROOT_USER       MINIO_ROOT_PASSWORD
#   GRAFANA_ADMIN_PASSWORD

set -euo pipefail

: "${NOMAD_TOKEN:?Must set NOMAD_TOKEN}"

echo "==> Storing Nomad Variables for abc-nodes services..."
echo "    Nomad token: ${NOMAD_TOKEN:0:8}..."
echo ""

# ─── MinIO root credentials ────────────────────────────────────────────────────
MINIO_ROOT_USER="${MINIO_ROOT_USER:-minio-admin}"
MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"

echo "==> [MinIO] Storing root credentials..."
printf "    user:     %s\n" "${MINIO_ROOT_USER}"
printf "    password: %s\n" "${MINIO_ROOT_PASSWORD}"
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-minio \
  minio_root_user="${MINIO_ROOT_USER}" \
  minio_root_password="${MINIO_ROOT_PASSWORD}"
# Loki and ntfy read MinIO creds from their own variable paths (ACL restriction)
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-loki \
  minio_access_key="${MINIO_ROOT_USER}" \
  minio_secret_key="${MINIO_ROOT_PASSWORD}"
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-ntfy \
  minio_access_key="${MINIO_ROOT_USER}" \
  minio_secret_key="${MINIO_ROOT_PASSWORD}"
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-tusd \
  minio_access_key="${MINIO_ROOT_USER}" \
  minio_secret_key="${MINIO_ROOT_PASSWORD}"
echo "    Done."
echo ""

# ─── Grafana admin password ────────────────────────────────────────────────────
GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-$(openssl rand -base64 16 | tr -d '/+=' | head -c 20)}"

echo "==> [Grafana] Storing admin password..."
printf "    password: %s\n" "${GRAFANA_ADMIN_PASSWORD}"
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-grafana \
  admin_password="${GRAFANA_ADMIN_PASSWORD}"
echo "    Done."
echo ""

# ─── Job notifier Nomad token ──────────────────────────────────────────────────
# The job-notifier needs a Nomad token to stream the event API.
# Using the caller's management token here; create a dedicated token for least-privilege.
JOB_NOTIFIER_TOKEN="${JOB_NOTIFIER_TOKEN:-${NOMAD_TOKEN}}"

echo "==> [Job Notifier] Storing Nomad event-stream token..."
printf "    token: %s...\n" "${JOB_NOTIFIER_TOKEN:0:8}"
abc admin services nomad cli -- var put -namespace services -force \
  nomad/jobs/abc-nodes-job-notifier \
  nomad_token="${JOB_NOTIFIER_TOKEN}"
echo "    Done."
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────
echo "=================================================================="
echo " Done! All Nomad Variables stored."
echo ""
echo " Next — run Supabase secrets (generates JWT tokens):"
echo "   bash deployments/abc-nodes/scripts/init-supabase-secrets.sh"
echo ""
echo " Then redeploy updated services:"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/minio.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/grafana.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/job-notifier.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/loki.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/ntfy.nomad.hcl"
echo "   abc admin services nomad cli -- job stop abc-nodes-postgres"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/supabase.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/wave.nomad.hcl"
echo "   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/traefik.nomad.hcl"
echo ""
echo " Initialize Vault (if not done):"
echo "   bash deployments/abc-nodes/scripts/init-vault.sh"
echo ""
echo " Rotate mc alias with new MinIO credentials:"
echo "   mc alias set sunminio http://100.70.185.46:9000 \\"
echo "     ${MINIO_ROOT_USER} ${MINIO_ROOT_PASSWORD}"
echo "=================================================================="
