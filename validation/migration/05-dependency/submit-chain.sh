#!/usr/bin/env bash
# submit-chain.sh — Portable dependency chaining via exit-code polling
#
# This script demonstrates the ABC equivalent of:
#   PBS:   qsub -W depend=afterok:$STEP1 job-step2.pbs
#   SLURM: sbatch --dependency=afterok:$STEP1 job-step2.slurm
#
# Usage:
#   ./submit-chain.sh [--region <region>] [--dry-run]

set -euo pipefail

ABC="${ABC_BIN:-abc}"
REGION="${1:-}"
DRY_RUN=false
POLL_INTERVAL=30

while [[ $# -gt 0 ]]; do
  case "$1" in
    --region)   REGION="$2"; shift ;;
    --dry-run)  DRY_RUN=true ;;
  esac
  shift
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REGION_FLAG=""
[[ -n "$REGION" ]] && REGION_FLAG="--region $REGION"

run_abc() { $ABC $REGION_FLAG "$@"; }

if [[ "$DRY_RUN" == "true" ]]; then
  echo "=== DRY-RUN: generating HCL only ==="
  run_abc job run "$SCRIPT_DIR/job-step1.abc.sh" --dry-run
  run_abc job run "$SCRIPT_DIR/job-step2.abc.sh" --dry-run
  exit 0
fi

# ── Submit step 1 ─────────────────────────────────────────────────────────────
echo "Submitting step 1 (alignment)..."
STEP1_OUT=$(run_abc job run "$SCRIPT_DIR/job-step1.abc.sh" --submit)
echo "$STEP1_OUT"

STEP1_ID=$(echo "$STEP1_OUT" | grep -oP '(?<=Nomad job ID\s{3})\S+' || true)
if [[ -z "$STEP1_ID" ]]; then
  echo "ERROR: could not parse job ID from step 1 output" >&2
  exit 1
fi
echo "Step 1 job: $STEP1_ID"

# ── Poll until step 1 completes ───────────────────────────────────────────────
echo "Waiting for step 1 to complete (polling every ${POLL_INTERVAL}s)..."
while true; do
  STATUS_EXIT=0
  run_abc job status "$STEP1_ID" || STATUS_EXIT=$?

  case "$STATUS_EXIT" in
    0) echo "Step 1 complete — submitting step 2."; break ;;
    1) echo "ERROR: step 1 failed (exit 1)." >&2; exit 1 ;;
    2) echo "  still running... sleeping ${POLL_INTERVAL}s"; sleep "$POLL_INTERVAL" ;;
    3) echo "ERROR: cannot reach Nomad." >&2; exit 1 ;;
    *) echo "ERROR: unexpected exit $STATUS_EXIT" >&2; exit 1 ;;
  esac
done

# ── Submit step 2 ─────────────────────────────────────────────────────────────
echo "Submitting step 2 (variant calling)..."
STEP2_OUT=$(run_abc job run "$SCRIPT_DIR/job-step2.abc.sh" --submit)
echo "$STEP2_OUT"

STEP2_ID=$(echo "$STEP2_OUT" | grep -oP '(?<=Nomad job ID\s{3})\S+' || true)
echo "Step 2 job: $STEP2_ID"
echo
echo "Monitor:"
echo "  abc job status $STEP2_ID"
echo "  abc job logs   $STEP2_ID --follow"
