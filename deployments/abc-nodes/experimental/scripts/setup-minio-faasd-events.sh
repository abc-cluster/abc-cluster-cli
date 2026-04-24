#!/usr/bin/env bash
# setup-minio-faasd-events.sh
#
# Wires MinIO bucket event notifications → faasd OpenFaaS functions.
# Run after both abc-nodes-minio and abc-nodes-faasd are healthy.
#
# What this does:
#   1. Registers a webhook endpoint (the faasd gateway) in MinIO's config
#   2. Attaches PUT/DELETE event notifications to the specified buckets
#   3. Restarts MinIO to apply the new config
#
# Prerequisites:
#   • mc (MinIO client) at ~/.abc/binaries/mc
#   • sunminio alias already configured (done by apply-su-mbhg.sh)
#   • faasd gateway reachable at FAASD_URL (port 8089)
#   • Function already deployed to faasd (faas-cli deploy)
#
# Usage:
#   FUNCTION=my-processor BUCKET=su-mbhg-bioinformatics \
#     bash deployments/abc-nodes/experimental/scripts/setup-minio-faasd-events.sh
#
# To add multiple functions/buckets, call multiple times with different args,
# or edit the EVENTS array below.

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────
MINIO_ALIAS="${MINIO_ALIAS:-sunminio}"
FAASD_URL="${FAASD_URL:-http://100.70.185.46:8089}"
MCLI="${HOME}/.abc/binaries/mc"

# Function → bucket event mappings.
# Format: "function_name|bucket_name|event_types"
# event_types: comma-separated from: put,delete,get,replica,ilm,scanner
EVENTS=(
  # Trigger 'pipeline-trigger' function when any file lands in either group bucket
  "pipeline-trigger|su-mbhg-bioinformatics|put"
  "pipeline-trigger|su-mbhg-hostgen|put"

  # Trigger 'file-cleanup' on deletion (optional — add your own)
  # "file-cleanup|su-mbhg-bioinformatics|delete"
)

# ─────────────────────────────────────────────────────────────────────────────
# Validate
# ─────────────────────────────────────────────────────────────────────────────
if [[ ! -x "$MCLI" ]]; then
  echo "ERROR: mc not found at ${MCLI}" >&2
  echo "  Install: curl -sL https://dl.min.io/client/mc/release/linux-amd64/mc \\" >&2
  echo "           -o ~/.abc/binaries/mc && chmod +x ~/.abc/binaries/mc" >&2
  exit 1
fi

echo "==> Target MinIO alias : ${MINIO_ALIAS}"
echo "==> faasd gateway URL  : ${FAASD_URL}"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Step 1 — Collect unique function names and register each as a webhook target
# ─────────────────────────────────────────────────────────────────────────────
declare -A REGISTERED_WEBHOOKS

echo "==> [MinIO] Registering webhook endpoints..."
for entry in "${EVENTS[@]}"; do
  IFS='|' read -r func bucket events <<< "$entry"
  webhook_key="faasd_${func}"

  if [[ -n "${REGISTERED_WEBHOOKS[$webhook_key]+_}" ]]; then
    echo "    SKIP (already registered): ${webhook_key}"
    continue
  fi

  endpoint="${FAASD_URL}/function/${func}"
  queue_dir="/tmp/minio-faasd-${func}-queue"

  echo "    Registering notify_webhook:${webhook_key} → ${endpoint}"
  "$MCLI" admin config set "${MINIO_ALIAS}" \
    "notify_webhook:${webhook_key}" \
    "endpoint=${endpoint}" \
    "queue_dir=${queue_dir}" \
    "queue_limit=10000" \
    "enable=on"

  REGISTERED_WEBHOOKS[$webhook_key]=1
done
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Step 2 — Restart MinIO to pick up the new webhook config
# ─────────────────────────────────────────────────────────────────────────────
echo "==> [MinIO] Restarting to apply webhook config..."
"$MCLI" admin service restart "${MINIO_ALIAS}"
echo "    Waiting 5s for MinIO to come back up..."
sleep 5
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Step 3 — Attach event notifications to buckets
# ─────────────────────────────────────────────────────────────────────────────
echo "==> [MinIO] Attaching bucket event notifications..."
for entry in "${EVENTS[@]}"; do
  IFS='|' read -r func bucket events <<< "$entry"
  webhook_key="faasd_${func}"
  arn="arn:minio:sqs::${webhook_key}:webhook"

  # Convert comma-separated events to --event flags
  event_flags=""
  IFS=',' read -ra event_list <<< "$events"
  for e in "${event_list[@]}"; do
    event_flags="${event_flags} --event ${e}"
  done

  echo "    ${bucket}  [${events}]  →  function/${func}"
  # shellcheck disable=SC2086
  "$MCLI" event add "${MINIO_ALIAS}/${bucket}" "${arn}" ${event_flags} \
    2>&1 || echo "    (already attached or error — check manually)"
done
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Verification
# ─────────────────────────────────────────────────────────────────────────────
echo "==> [MinIO] Current event notifications per bucket:"
for entry in "${EVENTS[@]}"; do
  IFS='|' read -r func bucket events <<< "$entry"
  echo "    --- ${bucket} ---"
  "$MCLI" event list "${MINIO_ALIAS}/${bucket}" 2>&1 | sed 's/^/    /'
done
echo ""

echo "==> [faasd] Check gateway health:"
echo "    curl -si ${FAASD_URL}/healthz"
echo ""
echo "==> [faasd] List deployed functions:"
echo "    curl -su admin:\$(cat /var/lib/faasd/secrets/basic-auth-password) \\"
echo "         ${FAASD_URL}/system/functions | python3 -m json.tool"
echo ""
echo "==================================================================="
echo " Done!  MinIO bucket events are now wired to faasd functions."
echo ""
echo " To deploy a function:"
echo "   faas-cli deploy --image=<image> --name=pipeline-trigger \\"
echo "     --gateway=${FAASD_URL}"
echo ""
echo " To test an event manually (upload a file):"
echo "   mc cp <local-file> ${MINIO_ALIAS}/su-mbhg-bioinformatics/test/"
echo "   # Then check faasd logs:"
echo "   # journalctl -u faasd (if systemd)  OR  nomad alloc logs <alloc>"
echo "==================================================================="
