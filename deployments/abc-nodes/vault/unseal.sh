#!/bin/sh
# unseal.sh — Auto-unseal Vault using key shares from environment.
#
# Invoked by systemd as ExecStartPost= in vault.service.
# The three vars VAULT_UNSEAL_KEY_1/2/3 are injected by systemd from
# /etc/vault.d/vault.env (the vault process itself never reads that file).
#
# Behaviour:
#   • Polls the API until it responds (up to 60 s).
#   • If already unsealed, exits 0 immediately (idempotent).
#   • Submits key shares 1, 2, 3 in sequence.
#   • Logs to stdout → journald (journalctl -u vault -f).

set -e

VAULT_ADDR="http://127.0.0.1:8200"
export VAULT_ADDR

log() { echo "vault-unseal: $*"; }

# ── Wait for API ──────────────────────────────────────────────────────────────
for i in $(seq 1 30); do
  STATUS=$(curl -sf -o /dev/null -w '%{http_code}' \
    "${VAULT_ADDR}/v1/sys/health?sealedcode=200&uninitcode=200&standbyok=true" \
    2>/dev/null || echo "000")
  [ "$STATUS" != "000" ] && break
  log "waiting for API... ($i/30)"
  sleep 2
done

if [ "$STATUS" = "000" ]; then
  log "ERROR: Vault API did not come up within 60 s — check 'journalctl -u vault'"
  exit 1
fi

log "API is up (HTTP ${STATUS})"

# ── Check seal status ─────────────────────────────────────────────────────────
SEALED=$(curl -sf "${VAULT_ADDR}/v1/sys/seal-status" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['sealed'])" \
  2>/dev/null || echo "true")

if [ "$SEALED" = "False" ] || [ "$SEALED" = "false" ]; then
  log "already unsealed — nothing to do"
  exit 0
fi

log "sealed — submitting 3 key shares..."

_unseal() {
  local key="$1"
  curl -sf -X PUT "${VAULT_ADDR}/v1/sys/unseal" \
    -H 'Content-Type: application/json' \
    -d "{\"key\": \"${key}\"}" \
    | python3 -c "
import sys, json
d = json.load(sys.stdin)
print('  progress {}/{} sealed={}'.format(d.get('progress',0), d.get('t',3), d.get('sealed','?')))
"
}

_unseal "${VAULT_UNSEAL_KEY_1}"
_unseal "${VAULT_UNSEAL_KEY_2}"
_unseal "${VAULT_UNSEAL_KEY_3}"

log "done"
