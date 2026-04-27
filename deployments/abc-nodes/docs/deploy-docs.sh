#!/usr/bin/env bash
set -euo pipefail

# Push the built Docusaurus site (website/build) to the abc-nodes "scratch"
# host volume on aither, where the abc-nodes-docs Nomad job serves it via
# Caddy at http://docs.aither.
#
# Caddy reads files on every request — content updates appear immediately
# without restarting the job.
#
# Defaults are tuned for sun-aither.  Override via env vars, e.g.:
#   REMOTE_HOST=sun-aither REMOTE_DOCS_DIR=/opt/nomad/scratch/abc-docs ./deploy-docs.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# CLI package root (two levels up from deployments/abc-nodes/docs).
CLI_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
LOCAL_BUILD_DIR="${LOCAL_BUILD_DIR:-${CLI_DIR}/website/build}"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"

REMOTE_TMP_DIR="${REMOTE_TMP_DIR:-/tmp/abc-docs-staging}"
REMOTE_DOCS_DIR="${REMOTE_DOCS_DIR:-/opt/nomad/scratch/abc-docs}"

# ── Preflight ────────────────────────────────────────────────────────────────
if [[ ! -f "${LOCAL_BUILD_DIR}/index.html" ]]; then
  echo "ERROR: ${LOCAL_BUILD_DIR}/index.html not found." >&2
  echo "       Run 'just docs-build' first." >&2
  exit 1
fi
[[ -f "${PASS_FILE}" ]] || { echo "ERROR: not found: ${PASS_FILE}" >&2; exit 1; }

SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
PASS="$(cat "${PASS_FILE}")"

# ── Transfer to staging ──────────────────────────────────────────────────────
echo "==> Staging build to ${REMOTE_HOST}:${REMOTE_TMP_DIR}"
${SSH} "rm -rf '${REMOTE_TMP_DIR}' && mkdir -p '${REMOTE_TMP_DIR}'"
${SCP} -r "${LOCAL_BUILD_DIR}/." "${REMOTE_HOST}:${REMOTE_TMP_DIR}/"

# ── Atomic swap into the host volume ─────────────────────────────────────────
echo "==> Installing into ${REMOTE_DOCS_DIR} on ${REMOTE_HOST}"
echo "${PASS}" | ${SSH} "sudo -S bash -c \
  \"rm -rf '${REMOTE_DOCS_DIR}' && \
    mkdir -p \\\"\\\$(dirname '${REMOTE_DOCS_DIR}')\\\" && \
    mv '${REMOTE_TMP_DIR}' '${REMOTE_DOCS_DIR}'\""

echo "==> Done.  Site available at http://docs.aither/ once the abc-nodes-docs job is running."
echo "    First-time setup:  just docs-job-run"
