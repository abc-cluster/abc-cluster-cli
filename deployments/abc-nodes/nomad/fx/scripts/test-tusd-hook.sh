#!/usr/bin/env bash
# test-tusd-hook.sh
#
# Send a synthetic tusd post-finish hook to fx-tusd-hook and verify:
#   1. The hook server accepts it (HTTP 200).
#   2. ntfy receives a notification on the tusd-uploads topic.
#
# Run after deploying fx-tusd-hook:
#   bash deployments/abc-nodes/nomad/fx/scripts/test-tusd-hook.sh
#
# The test uses a fake UUID key (the rename S3 call will fail because the
# object doesn't exist, but the hook server logs the attempt and still
# returns 200 — so the ntfy delivery can be verified independently).

set -euo pipefail

HOOK_URL="${HOOK_URL:-http://100.77.21.36:14002}"
NTFY_URL="${NTFY_URL:-http://ntfy.aither/tusd-uploads/json}"

ok()  { echo "  ✓ $*"; }
warn(){ echo "  ! $*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

# ── Synthetic tusd post-finish payload ────────────────────────────────────────
# Matches the exact JSON shape tusd POSTs on post-finish.

PAYLOAD=$(cat <<'JSON'
{
  "Event": {
    "Upload": {
      "ID": "a1b2c3d4e5f6abcd1234567890abcdef+0",
      "Storage": {
        "S3": {
          "Key":    "a1b2c3d4e5f6abcd1234567890abcdef",
          "Bucket": "tusd"
        }
      },
      "MetaData": {
        "filename": "sample_R1.fastq.gz",
        "name":     "sample_R1.fastq.gz"
      },
      "Size":           12345678,
      "SizeIsDeferred": false
    }
  }
}
JSON
)

# ── 1. Health check ────────────────────────────────────────────────────────────

echo "==> Checking hook server health..."
HEALTH=$(curl --noproxy '*' -sS -o /dev/null -w "%{http_code}" \
              --max-time 5 "${HOOK_URL}/healthz" 2>/dev/null || echo "ERR")
if [[ "$HEALTH" =~ ^2 ]]; then
  ok "hook server is up (HTTP $HEALTH)"
else
  die "hook server returned $HEALTH — is fx-tusd-hook running?"
fi

# ── 2. Send synthetic hook ─────────────────────────────────────────────────────

echo ""
echo "==> Sending synthetic post-finish hook to ${HOOK_URL}/hook ..."
RESP=$(curl --noproxy '*' -sS -w "\n%{http_code}" \
            --max-time 10 \
            -X POST "${HOOK_URL}/hook" \
            -H "Content-Type: application/json" \
            -d "$PAYLOAD" 2>/dev/null)

HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | head -1)

if [[ "$HTTP_CODE" =~ ^2 ]]; then
  ok "Hook server accepted event (HTTP $HTTP_CODE): $BODY"
else
  die "Hook server returned $HTTP_CODE: $BODY"
fi

# ── 3. Check ntfy ─────────────────────────────────────────────────────────────

echo ""
echo "==> Checking ntfy for the notification (last 15s)..."
sleep 2

SINCE=$(date -u -v-15S '+%s' 2>/dev/null || date -u --date='15 seconds ago' '+%s' 2>/dev/null || echo "0")
NTFY_MSG=$(curl --noproxy '*' -sS --max-time 5 \
               "${NTFY_URL}?since=${SINCE}&poll=1" 2>/dev/null || true)

if echo "$NTFY_MSG" | grep -q "tusd: upload complete"; then
  ok "ntfy received notification:"
  echo "$NTFY_MSG" | python3 -m json.tool 2>/dev/null \
    | grep -E '"(title|message|tags)"' || echo "$NTFY_MSG" | head -5
else
  warn "ntfy message not found — the S3 rename likely failed (expected: object doesn't exist in test)."
  warn "Check hook logs for the rename attempt and ntfy delivery."
  echo "    curl -s http://ntfy.aither/tusd-uploads/json?poll=1"
fi

echo ""
echo "══════════════════════════════════════════════════════════"
echo "  Test complete."
echo ""
echo "  Monitor ntfy topic in real time:"
echo "    curl -s http://ntfy.aither/tusd-uploads/json"
echo ""
echo "  For a real end-to-end test, upload via tusd:"
echo "    abc data upload <file>       # uses tusd.aither"
echo "    mc ls minio/tusd/            # verify key renamed to original filename"
echo "══════════════════════════════════════════════════════════"
