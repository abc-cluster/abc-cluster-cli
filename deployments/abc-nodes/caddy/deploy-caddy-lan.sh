#!/usr/bin/env bash
set -euo pipefail

# Deploy Caddy LAN config to a remote host and validate.
#
# Defaults are tuned for sun-aither.
# Override as needed, e.g.:
#   REMOTE_HOST=sun-aither \
#   PASS_FILE=~/.ssh/pass.sun-aither \
#   DOMAIN=aither.mb.sun.ac.za \
#   DOMAIN_IP=146.232.174.77 \
#   ./deploy-caddy-lan.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_CADDYFILE="${LOCAL_CADDYFILE:-${SCRIPT_DIR}/Caddyfile.lan}"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"

REMOTE_TMP_CADDYFILE="${REMOTE_TMP_CADDYFILE:-/tmp/Caddyfile.lan}"
REMOTE_CADDYFILE="${REMOTE_CADDYFILE:-/etc/caddy/Caddyfile.lan}"

DOMAIN="${DOMAIN:-aither.mb.sun.ac.za}"
DOMAIN_IP="${DOMAIN_IP:-146.232.174.77}"

if [[ ! -f "${LOCAL_CADDYFILE}" ]]; then
  echo "ERROR: local Caddyfile not found: ${LOCAL_CADDYFILE}" >&2
  exit 1
fi

if [[ ! -f "${PASS_FILE}" ]]; then
  echo "ERROR: password file not found: ${PASS_FILE}" >&2
  exit 1
fi

echo "==> Copying ${LOCAL_CADDYFILE} to ${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"
sshpass -f "${PASS_FILE}" scp -o StrictHostKeyChecking=no \
  "${LOCAL_CADDYFILE}" "${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"

echo "==> Installing, validating, and reloading Caddy on ${REMOTE_HOST}"
sshpass -f "${PASS_FILE}" ssh -o StrictHostKeyChecking=no "${REMOTE_HOST}" \
  "sudo -S cp '${REMOTE_TMP_CADDYFILE}' '${REMOTE_CADDYFILE}' && \
   sudo -S caddy validate --config '${REMOTE_CADDYFILE}' && \
   sudo -S caddy reload --config '${REMOTE_CADDYFILE}'" < "${PASS_FILE}"

echo "==> Smoke test: http://${DOMAIN}/"
curl --noproxy '*' -I --resolve "${DOMAIN}:80:${DOMAIN_IP}" "http://${DOMAIN}/"

echo "==> Done."
