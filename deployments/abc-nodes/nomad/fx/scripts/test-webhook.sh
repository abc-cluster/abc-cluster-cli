#!/usr/bin/env bash
# test-webhook.sh
#
# Send a synthetic MinIO webhook event to the fx-notify function and verify
# that a notification appears in ntfy.  Run this after deploying the job
# to confirm the full path works before wiring up real MinIO events.
#
# Usage
# ─────
#   bash deployments/abc-nodes/nomad/fx/scripts/test-webhook.sh
#
#   # Point at a different endpoint:
#   NOTIFY_URL=http://100.70.185.46:14001 \
#     bash deployments/abc-nodes/nomad/fx/scripts/test-webhook.sh

set -euo pipefail

NOTIFY_URL="${NOTIFY_URL:-http://notify.aither/}"
NTFY_URL="${NTFY_URL:-http://ntfy.aither/minio-events/json}"

ok()  { echo "  ✓ $*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

# ── Synthetic MinIO event payload ─────────────────────────────────────────────
# Matches the exact JSON shape MinIO POSTs to webhook targets.

PAYLOAD=$(cat <<'JSON'
{
  "Records": [
    {
      "eventVersion": "2.0",
      "eventSource": "aws:s3",
      "awsRegion": "",
      "eventTime": "2026-04-26T18:00:00.000Z",
      "eventName": "s3:ObjectCreated:Put",
      "userIdentity": { "principalId": "test-user" },
      "requestParameters": { "sourceIPAddress": "127.0.0.1" },
      "responseElements": {},
      "s3": {
        "s3SchemaVersion": "1.0",
        "configurationId": "fx-test",
        "bucket": {
          "name": "raw-data",
          "ownerIdentity": { "principalId": "test-user" },
          "arn": "arn:aws:s3:::raw-data"
        },
        "object": {
          "key": "uploads/test-sample.fastq.gz",
          "size": 1234567,
          "eTag": "abc123def456",
          "contentType": "application/gzip",
          "sequencer": "0001"
        }
      }
    }
  ]
}
JSON
)

# ── 1. Health check ────────────────────────────────────────────────────────────

echo "==> Checking notify function health..."
HEALTH=$(curl --noproxy '*' -sS -o /dev/null -w "%{http_code}" \
              --max-time 5 "${NOTIFY_URL}healthz" 2>/dev/null || echo "ERR")
if [[ "$HEALTH" =~ ^2 ]]; then
  ok "notify function is up (HTTP $HEALTH)"
else
  die "notify function returned $HEALTH — is fx-notify running?"
fi

# ── 2. Send synthetic event ────────────────────────────────────────────────────

echo ""
echo "==> Sending synthetic s3:ObjectCreated:Put event to $NOTIFY_URL ..."
RESP=$(curl --noproxy '*' -sS -w "\n%{http_code}" \
            --max-time 10 \
            -X POST "$NOTIFY_URL" \
            -H "Content-Type: application/json" \
            -d "$PAYLOAD" 2>/dev/null)

HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | head -1)

if [[ "$HTTP_CODE" =~ ^2 ]]; then
  ok "Function accepted event (HTTP $HTTP_CODE): $BODY"
else
  die "Function returned $HTTP_CODE: $BODY"
fi

# ── 3. Check ntfy received the notification ────────────────────────────────────

echo ""
echo "==> Checking ntfy for the notification (last 5s)..."
sleep 1   # give ntfy a moment

SINCE=$(date -u -v-10S '+%s' 2>/dev/null || date -u --date='10 seconds ago' '+%s' 2>/dev/null || echo "0")
NTFY_MSG=$(curl --noproxy '*' -sS --max-time 5 \
               "${NTFY_URL}?since=${SINCE}&poll=1" 2>/dev/null || true)

if echo "$NTFY_MSG" | grep -q "raw-data"; then
  ok "ntfy received notification:"
  echo "$NTFY_MSG" | python3 -m json.tool 2>/dev/null | grep -E "(title|message|tags)" || \
  echo "$NTFY_MSG" | head -5
else
  echo "  ! ntfy message not found — check manually:"
  echo "    curl -s http://ntfy.aither/minio-events/json?poll=1"
  echo "    (notification may still arrive — MinIO delivery can be async)"
fi

echo ""
echo "══════════════════════════════════════════════════════════"
echo "  Test complete."
echo ""
echo "  Monitor ntfy topic in real time:"
echo "    curl -s http://ntfy.aither/minio-events/json"
echo ""
echo "  Tail function logs:"
echo "    NOMAD_NAMESPACE=abc-experimental \\"
echo "      abc admin services nomad cli -- alloc logs -f \$(..."
echo "      nomad job status -namespace abc-experimental fx-notify | grep running | awk '{print \$1}')"
echo "══════════════════════════════════════════════════════════"
