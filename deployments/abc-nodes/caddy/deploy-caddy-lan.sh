#!/usr/bin/env bash
set -euo pipefail

# Deploy Caddy LAN config to a remote host and validate.
#
# Architecture: vhost-per-service (*.aither) + institutional host (aither.mb.sun.ac.za).
# Backends are resolved via Consul DNS — run deploy-consul.sh first.
#
# Defaults are tuned for sun-aither.
# Override as needed, e.g.:
#   REMOTE_HOST=sun-aither DOMAIN_IP=146.232.174.77 ./deploy-caddy-lan.sh

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
# Tailscale IP — used for resolving *.aither vhosts in smoke tests.
AITHER_TS_IP="${AITHER_TS_IP:-100.70.185.46}"

# ── Preflight checks ──────────────────────────────────────────────────────────
for f in "${LOCAL_CADDYFILE}" "${LOCAL_LANDING_DIR}/index.html" "${PASS_FILE}"; do
  [[ -f "$f" ]] || { echo "ERROR: not found: $f" >&2; exit 1; }
done

SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
PASS="$(cat "${PASS_FILE}")"

# ── Transfer ──────────────────────────────────────────────────────────────────
echo "==> Copying Caddyfile to ${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"
${SCP} "${LOCAL_CADDYFILE}" "${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"

echo "==> Copying landing assets to ${REMOTE_HOST}:${REMOTE_TMP_LANDING_DIR}"
${SSH} "rm -rf '${REMOTE_TMP_LANDING_DIR}' && mkdir -p '${REMOTE_TMP_LANDING_DIR}'"
${SCP} -r "${LOCAL_LANDING_DIR}/." "${REMOTE_HOST}:${REMOTE_TMP_LANDING_DIR}/"

# ── Install, validate, reload ─────────────────────────────────────────────────
echo "==> Installing, validating, and reloading Caddy on ${REMOTE_HOST}"
echo "${PASS}" | ${SSH} "sudo -S bash -c \
  \"cp '${REMOTE_TMP_CADDYFILE}' '${REMOTE_CADDYFILE}' && \
    mkdir -p '${REMOTE_LANDING_DIR}' && \
    cp -r '${REMOTE_TMP_LANDING_DIR}/.' '${REMOTE_LANDING_DIR}/' && \
    caddy validate --config '${REMOTE_CADDYFILE}' && \
    caddy reload  --config '${REMOTE_CADDYFILE}'\""

# ── Helpers ───────────────────────────────────────────────────────────────────
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); echo "  [PASS] $*"; }
nok() { FAIL=$((FAIL+1)); echo "  [FAIL] $*"; }

check_code() {
  local label="$1" want="$2"
  shift 2
  local got
  got=$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' "$@")
  if [[ "${got}" == "${want}" ]]; then
    ok "${label}  →  HTTP ${got}"
  else
    nok "${label}  →  got ${got}  want ${want}"
  fi
}

check_location() {
  local label="$1" want_pattern="$2"
  shift 2
  local loc
  loc=$(curl --noproxy '*' -sS -o /dev/null -w '%{redirect_url}' "$@")
  if [[ "${loc}" == ${want_pattern} ]]; then
    ok "${label}  →  ${loc}"
  else
    nok "${label}  →  got '${loc}'  want '${want_pattern}'"
  fi
}

R="--resolve ${DOMAIN}:80:${DOMAIN_IP}"

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Caddy smoke tests  (${DOMAIN} → ${DOMAIN_IP})"
echo "══════════════════════════════════════════════════════"

# ── Landing page ──────────────────────────────────────────────────────────────
echo ""
echo "  ── Landing & Nomad UI ───────────────────────────────"
check_code "GET /  (landing page)" "200" ${R} "http://${DOMAIN}/"
check_code "GET /ui/  (Nomad native)" "200|301|307|308" ${R} "http://${DOMAIN}/ui/"

# ── /services/... → *.aither redirects ───────────────────────────────────────
echo ""
echo "  ── /services/* → *.aither redirects ────────────────"
check_code   "/services/grafana"            "308" ${R} "http://${DOMAIN}/services/grafana"
check_location "/services/grafana location" "http://grafana.aither*" ${R} "http://${DOMAIN}/services/grafana"

check_code   "/services/grafana/d/foo (prefix strip)" "308" ${R} "http://${DOMAIN}/services/grafana/d/foo"
check_location "  location strips prefix" "http://grafana.aither/d/foo" ${R} "http://${DOMAIN}/services/grafana/d/foo"

check_code   "/services/ntfy/"        "308" ${R} "http://${DOMAIN}/services/ntfy/"
check_location "  ntfy → ntfy.aither" "http://ntfy.aither*" ${R} "http://${DOMAIN}/services/ntfy/"

check_code   "/services/prometheus/"  "308" ${R} "http://${DOMAIN}/services/prometheus/"
check_location "  prometheus location" "http://prometheus.aither*" ${R} "http://${DOMAIN}/services/prometheus/"

check_code   "/services/minio/minio-console/" "308" ${R} "http://${DOMAIN}/services/minio/minio-console/"
check_location "  minio-console location" "http://minio-console.aither*" ${R} "http://${DOMAIN}/services/minio/minio-console/"

check_code   "/services/minio/" "308" ${R} "http://${DOMAIN}/services/minio/"
check_location "  minio location" "http://minio.aither*" ${R} "http://${DOMAIN}/services/minio/"

check_code   "/services/loki/"  "308" ${R} "http://${DOMAIN}/services/loki/"
check_code   "/services/tusd/"  "308" ${R} "http://${DOMAIN}/services/tusd/"
check_code   "/services/uppy/"  "308" ${R} "http://${DOMAIN}/services/uppy/"
check_code   "/services/rustfs/" "308" ${R} "http://${DOMAIN}/services/rustfs/"
check_code   "/services/grafana-alloy/" "308" ${R} "http://${DOMAIN}/services/grafana-alloy/"
check_code   "/services/vault/"    "308" ${R} "http://${DOMAIN}/services/vault/"
check_code   "/services/boundary/" "308" ${R} "http://${DOMAIN}/services/boundary/"
check_code   "/services/consul/"  "308" ${R} "http://${DOMAIN}/services/consul/"
check_code   "/services/traefik/" "308" ${R} "http://${DOMAIN}/services/traefik/"

# ── Nomad subpath redirects ───────────────────────────────────────────────────
echo ""
echo "  ── Nomad redirects ──────────────────────────────────"
check_code   "/services/nomad/"       "308" ${R} "http://${DOMAIN}/services/nomad/"
check_location "  nomad → nomad.aither" "http://nomad.aither*" ${R} "http://${DOMAIN}/services/nomad/"

# ── Legacy bare paths ─────────────────────────────────────────────────────────
echo ""
echo "  ── Legacy bare paths ────────────────────────────────"
check_code     "/grafana/  (legacy)"   "308" ${R} "http://${DOMAIN}/grafana/"
check_location "  → grafana.aither"   "http://grafana.aither*" ${R} "http://${DOMAIN}/grafana/"

check_code     "/ntfy/  (legacy)"     "308" ${R} "http://${DOMAIN}/ntfy/"
check_location "  → ntfy.aither"     "http://ntfy.aither*" ${R} "http://${DOMAIN}/ntfy/"

check_code     "/prometheus/ (legacy)" "308" ${R} "http://${DOMAIN}/prometheus/"
check_code     "/minio/ (legacy)"      "308" ${R} "http://${DOMAIN}/minio/"
check_code     "/loki/ (legacy)"       "308" ${R} "http://${DOMAIN}/loki/"
check_code     "/traefik/ (legacy)"     "308" ${R} "http://${DOMAIN}/traefik/"

# ── /static/ not globally hijacked ───────────────────────────────────────────
echo ""
echo "  ── Global path hygiene ──────────────────────────────"
check_code "/static/foo without Referer → 404" "404" ${R} "http://${DOMAIN}/static/foo"
check_code "/unknown-path → 404"               "404" ${R} "http://${DOMAIN}/this-does-not-exist"

# ── *.aither vhosts reachable (Caddy accepts Host header) ────────────────────
# Services may return 502 if Consul hasn't registered them yet — that's expected
# at deploy time. We just verify Caddy doesn't return 421 (misdirected) or 400.
echo ""
echo "  ── *.aither vhost acceptance (Caddy config, not service health) ──"
VHOSTS=(grafana loki prometheus minio minio-console ntfy tusd uppy alloy rustfs vault boundary consul traefik nomad)
for vhost in "${VHOSTS[@]}"; do
  # Resolve the vhost to DOMAIN_IP — works before dnsmasq is propagated to this machine.
  code=$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' \
    --resolve "${vhost}.aither:80:${DOMAIN_IP}" \
    "http://${vhost}.aither/" 2>/dev/null || echo "000")
  if [[ "${code}" == "400" || "${code}" == "421" || "${code}" == "000" ]]; then
    nok "${vhost}.aither  →  HTTP ${code}  (Caddy rejected or unreachable)"
  else
    ok  "${vhost}.aither  →  HTTP ${code}  (Caddy accepted, backend may be pending)"
  fi
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "══════════════════════════════════════════════════════"
printf "  Results: %d passed,  %d failed\n" "${PASS}" "${FAIL}"
echo "══════════════════════════════════════════════════════"
[[ "${FAIL}" -eq 0 ]] || exit 1
echo "==> Done."
