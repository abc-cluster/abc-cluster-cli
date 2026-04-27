#!/usr/bin/env bash
set -euo pipefail

# Deploy minimal Caddyfile.lan to the systemd Caddy, freeing port 80 on the LAN IP.
#
# Architecture (post-consolidation):
#   HTTP routing is handled entirely by the abc-experimental-caddy-tailscale Nomad job,
#   which binds BOTH 146.232.174.77 (LAN) and 100.70.185.46 (Tailscale) on port 80.
#
#   This script deploys the minimal Caddyfile.lan (no vhosts) so the systemd Caddy
#   stays running without consuming port 80 and without conflicting with the Nomad job.
#
#   After running this script, deploy the Nomad Caddy job:
#     abc admin services nomad cli -- job run \
#       deployments/abc-nodes/nomad/experimental/caddy-tailscale.nomad.hcl
#
# Usage:
#   REMOTE_HOST=sun-aither ./deploy-caddy-lan.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_CADDYFILE="${LOCAL_CADDYFILE:-${SCRIPT_DIR}/Caddyfile.lan}"

REMOTE_HOST="${REMOTE_HOST:-sun-aither}"
PASS_FILE="${PASS_FILE:-$HOME/.ssh/pass.sun-aither}"

REMOTE_TMP_CADDYFILE="${REMOTE_TMP_CADDYFILE:-/tmp/Caddyfile.lan}"
REMOTE_CADDYFILE="${REMOTE_CADDYFILE:-/etc/caddy/Caddyfile.lan}"

# ── Preflight checks ──────────────────────────────────────────────────────────
for f in "${LOCAL_CADDYFILE}" "${PASS_FILE}"; do
  [[ -f "$f" ]] || { echo "ERROR: not found: $f" >&2; exit 1; }
done

SSH="sshpass -f ${PASS_FILE} ssh -o StrictHostKeyChecking=no ${REMOTE_HOST}"
SCP="sshpass -f ${PASS_FILE} scp -o StrictHostKeyChecking=no"
PASS="$(cat "${PASS_FILE}")"

# ── Transfer ──────────────────────────────────────────────────────────────────
echo "==> Copying minimal Caddyfile.lan to ${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"
${SCP} "${LOCAL_CADDYFILE}" "${REMOTE_HOST}:${REMOTE_TMP_CADDYFILE}"

# ── Install, validate, reload ─────────────────────────────────────────────────
echo "==> Installing and reloading systemd Caddy on ${REMOTE_HOST}"
echo "${PASS}" | ${SSH} "sudo -S bash -c \
  \"cp '${REMOTE_TMP_CADDYFILE}' '${REMOTE_CADDYFILE}' && \
    caddy validate --config '${REMOTE_CADDYFILE}' && \
    caddy reload  --config '${REMOTE_CADDYFILE}'\""

echo ""
echo "==> Systemd Caddy reloaded. Port 80 on 146.232.174.77 is now free."
echo ""
echo "==> Next: deploy the Nomad Caddy job to take over both LAN + Tailscale surfaces:"
echo "      abc admin services nomad cli -- job run \\"
echo "        deployments/abc-nodes/nomad/experimental/caddy-tailscale.nomad.hcl"
echo ""
echo "==> Done."
