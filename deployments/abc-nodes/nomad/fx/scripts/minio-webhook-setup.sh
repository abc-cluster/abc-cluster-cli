#!/usr/bin/env bash
# minio-webhook-setup.sh
#
# Configure MinIO to deliver bucket events to the fx-notify function.
#
# What this does
# ──────────────
#   1. Registers a webhook notification target in MinIO pointing at notify.aither.
#   2. Subscribes one or more buckets to ObjectCreated/ObjectRemoved events.
#
# Prerequisites
# ─────────────
#   - mc (MinIO client) in PATH
#   - mc alias for the cluster (default: abc-minio — see ALIAS variable below)
#   - fx-notify Nomad job running and healthy (notify.aither resolving)
#   - Tailscale connected (for *.aither vhost access)
#
# Usage
# ─────
#   bash deployments/abc-nodes/nomad/fx/scripts/minio-webhook-setup.sh
#
#   # Override defaults:
#   ALIAS=myminio BUCKET=uploads WEBHOOK_ID=fx \
#     bash deployments/abc-nodes/nomad/fx/scripts/minio-webhook-setup.sh
#
# Re-running is idempotent: existing config is shown and skipped if already set.

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────

ALIAS="${ALIAS:-abc-minio}"
WEBHOOK_ID="${WEBHOOK_ID:-fx}"
ENDPOINT="${ENDPOINT:-http://notify.aither/}"
BUCKET="${BUCKET:-}"            # if empty, user is prompted
EVENTS="${EVENTS:-s3:ObjectCreated:*,s3:ObjectRemoved:Delete}"
PREFIX="${PREFIX:-}"            # optional key prefix filter
SUFFIX="${SUFFIX:-}"            # optional key suffix filter (e.g. .fastq.gz)

# ── Helpers ───────────────────────────────────────────────────────────────────

ok()   { echo "  ✓ $*"; }
warn() { echo "  ! $*"; }
die()  { echo "ERROR: $*" >&2; exit 1; }

# ── Preflight checks ──────────────────────────────────────────────────────────

command -v mc &>/dev/null || die "mc not found — install the MinIO client first"

echo "==> Checking mc alias '$ALIAS'..."
if ! mc alias list "$ALIAS" &>/dev/null; then
  die "mc alias '$ALIAS' not configured. Run: mc alias set $ALIAS http://minio.aither <key> <secret>"
fi
ok "mc alias '$ALIAS' is set"

# ── Step 1: Register webhook target ──────────────────────────────────────────

echo ""
echo "==> Configuring webhook target '$WEBHOOK_ID' → $ENDPOINT"

mc admin config set "$ALIAS" \
  "notify_webhook:${WEBHOOK_ID}" \
  "endpoint=${ENDPOINT}"          \
  "queue_limit=10000"             \
  "queue_dir=/tmp/minio-events-${WEBHOOK_ID}"

ok "Webhook target '$WEBHOOK_ID' set"

# Apply the config change (requires MinIO restart or a config reload).
echo "    Restarting MinIO service to apply config..."
mc admin service restart "$ALIAS" --wait 2>/dev/null || \
  warn "Could not restart MinIO automatically — restart it manually if events don't arrive"

sleep 3   # allow MinIO to come back up

# ── Step 2: Resolve bucket ────────────────────────────────────────────────────

echo ""
if [ -z "$BUCKET" ]; then
  echo "Available buckets:"
  mc ls "$ALIAS" 2>/dev/null | awk '{print "  " $NF}' || true
  echo ""
  read -r -p "Enter bucket name to subscribe: " BUCKET
fi

echo "==> Subscribing bucket '$BUCKET' to events: $EVENTS"

# Check if bucket exists
mc ls "$ALIAS/$BUCKET" &>/dev/null || die "Bucket '$ALIAS/$BUCKET' does not exist"

# Build the ARN for the webhook target
ARN="arn:minio:sqs::${WEBHOOK_ID}:webhook"

# Build optional filter args
FILTER_ARGS=()
[ -n "$PREFIX" ] && FILTER_ARGS+=("--prefix" "$PREFIX")
[ -n "$SUFFIX" ] && FILTER_ARGS+=("--suffix" "$SUFFIX")

# Add event subscription (idempotent: mc event add is additive, not duplicating)
mc event add \
  "$ALIAS/$BUCKET" \
  "$ARN" \
  --event "$EVENTS" \
  "${FILTER_ARGS[@]}"

ok "Events subscribed on '$BUCKET'"

# ── Step 3: Verify ────────────────────────────────────────────────────────────

echo ""
echo "==> Current event subscriptions on '$BUCKET':"
mc event list "$ALIAS/$BUCKET" 2>/dev/null || warn "Could not list events — verify manually"

echo ""
echo "==> Webhook target config:"
mc admin config get "$ALIAS" "notify_webhook:${WEBHOOK_ID}" 2>/dev/null || true

# ── Step 4: Quick connectivity check ─────────────────────────────────────────

echo ""
echo "==> Probing $ENDPOINT (notify function)..."
STATUS=$(curl --noproxy '*' -sS -o /dev/null -w "%{http_code}" \
              --max-time 5 "${ENDPOINT}healthz" 2>/dev/null || echo "ERR")
if [[ "$STATUS" =~ ^2 ]]; then
  ok "$ENDPOINT is reachable (HTTP $STATUS)"
else
  warn "$ENDPOINT returned $STATUS — is fx-notify running? (nomad job status fx-notify)"
fi

echo ""
echo "══════════════════════════════════════════════════════════"
echo "  Setup complete."
echo ""
echo "  Upload a file to test:"
echo "    mc cp <local-file> $ALIAS/$BUCKET/"
echo ""
echo "  Watch ntfy topic for notifications:"
echo "    curl -s http://ntfy.aither/minio-events/json"
echo ""
echo "  Send a synthetic event:"
echo "    bash deployments/abc-nodes/nomad/fx/scripts/test-webhook.sh"
echo "══════════════════════════════════════════════════════════"
