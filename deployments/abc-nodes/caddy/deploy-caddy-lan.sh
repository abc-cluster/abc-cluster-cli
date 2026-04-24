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
LOCAL_LANDING_DIR="${LOCAL_LANDING_DIR:-${SCRIPT_DIR}/landing}"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"

REMOTE_TMP_CADDYFILE="${REMOTE_TMP_CADDYFILE:-/tmp/Caddyfile.lan}"
REMOTE_TMP_LANDING_DIR="${REMOTE_TMP_LANDING_DIR:-/tmp/caddy-landing}"
REMOTE_CADDYFILE="${REMOTE_CADDYFILE:-/etc/caddy/Caddyfile.lan}"
REMOTE_LANDING_DIR="${REMOTE_LANDING_DIR:-/etc/caddy/landing}"

DOMAIN="${DOMAIN:-aither.mb.sun.ac.za}"
DOMAIN_IP="${DOMAIN_IP:-146.232.174.77}"

if [[ ! -f "${LOCAL_CADDYFILE}" ]]; then
  echo "ERROR: local Caddyfile not found: ${LOCAL_CADDYFILE}" >&2
  exit 1
fi

if [[ ! -f "${LOCAL_LANDING_DIR}/index.html" ]]; then
  echo "ERROR: landing page not found: ${LOCAL_LANDING_DIR}/index.html" >&2
  exit 1
fi

if [[ ! -f "${PASS_FILE}" ]]; then
  echo "ERROR: password file not found: ${PASS_FILE}" >&2
  exit 1
fi

echo "==> Copying ${LOCAL_CADDYFILE} to ${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"
sshpass -f "${PASS_FILE}" scp -o StrictHostKeyChecking=no \
  "${LOCAL_CADDYFILE}" "${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"

echo "==> Copying landing assets to ${REMOTE_HOST}:${REMOTE_TMP_LANDING_DIR}"
sshpass -f "${PASS_FILE}" ssh -o StrictHostKeyChecking=no "${REMOTE_HOST}" \
  "rm -rf '${REMOTE_TMP_LANDING_DIR}' && mkdir -p '${REMOTE_TMP_LANDING_DIR}'"
sshpass -f "${PASS_FILE}" scp -o StrictHostKeyChecking=no -r \
  "${LOCAL_LANDING_DIR}/." "${REMOTE_HOST}:${REMOTE_TMP_LANDING_DIR}/"

echo "==> Installing, validating, and reloading Caddy on ${REMOTE_HOST}"
sshpass -f "${PASS_FILE}" ssh -o StrictHostKeyChecking=no "${REMOTE_HOST}" \
  "sudo -S cp '${REMOTE_TMP_CADDYFILE}' '${REMOTE_CADDYFILE}' && \
   sudo -S mkdir -p '${REMOTE_LANDING_DIR}' && \
   sudo -S cp -r '${REMOTE_TMP_LANDING_DIR}/.' '${REMOTE_LANDING_DIR}/' && \
   sudo -S caddy validate --config '${REMOTE_CADDYFILE}' && \
   sudo -S caddy reload --config '${REMOTE_CADDYFILE}'" < "${PASS_FILE}"

echo "==> Smoke tests (Host -> ${DOMAIN_IP}, no proxy)"
curl --noproxy '*' -fsS -I --resolve "${DOMAIN}:80:${DOMAIN_IP}" "http://${DOMAIN}/" | head -n 5

echo "==> Smoke: legacy /grafana/ -> /services/grafana/"
code_g=$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "${DOMAIN}:80:${DOMAIN_IP}" "http://${DOMAIN}/grafana/")
if [[ "${code_g}" != "308" ]]; then
  echo "ERROR: expected 308 for legacy /grafana/, got ${code_g}" >&2
  exit 1
fi

echo "==> Smoke: /services/ntfy/ (canonical path)"
code_ntfy=$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "${DOMAIN}:80:${DOMAIN_IP}" "http://${DOMAIN}/services/ntfy/")
if [[ "${code_ntfy}" != "200" ]]; then
  echo "ERROR: expected 200 for /services/ntfy/, got ${code_ntfy}" >&2
  exit 1
fi

echo "==> Smoke: ntfy root assets use Referer from /services/ntfy/ (vs MinIO /static/*)"
ct=$(curl --noproxy '*' -sS -o /dev/null -w '%{content_type}' \
  -H "Referer: http://${DOMAIN}/services/ntfy/" \
  --resolve "${DOMAIN}:80:${DOMAIN_IP}" \
  "http://${DOMAIN}/static/images/favicon.ico")
if [[ "${ct}" != image/* ]]; then
  echo "ERROR: expected image/* for /static/... favicon with Referer from /services/ntfy/, got content_type=${ct:-<empty>}" >&2
  exit 1
fi

echo "==> Smoke: root /static/* without Referer is not globally hijacked"
code_static=$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "${DOMAIN}:80:${DOMAIN_IP}" "http://${DOMAIN}/static/images/favicon.ico")
if [[ "${code_static}" != "404" ]]; then
  echo "ERROR: expected 404 for /static/... without Referer, got ${code_static}" >&2
  exit 1
fi

echo "==> Done."
