#!/usr/bin/env bash
# run-tests.sh
#
# Submits all abc-nodes test jobs, waits for each to complete, and prints a
# consolidated pass/fail summary.
#
# Prerequisites:
#   NOMAD_TOKEN — management token (services namespace)
#   Test secrets populated: bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Usage:
#   export NOMAD_TOKEN=<token>
#   bash deployments/abc-nodes/scripts/run-tests.sh [--skip-auth] [--skip-upload]
#
# Options:
#   --skip-auth     Skip auth-forwardauth test (requires a valid Nomad token)
#   --skip-upload   Skip upload-tusd test (requires valid token + full TUS stack)
#   --skip-vault    Skip vault test (requires Vault to be unsealed with known token)
#   --skip-storage  Skip storage-minio test (requires mc on host)

set -euo pipefail

: "${NOMAD_TOKEN:?Must set NOMAD_TOKEN}"

SKIP_AUTH=0
SKIP_UPLOAD=0
SKIP_VAULT=0
SKIP_STORAGE=0

for arg in "$@"; do
  case "$arg" in
    --skip-auth)    SKIP_AUTH=1 ;;
    --skip-upload)  SKIP_UPLOAD=1 ;;
    --skip-vault)   SKIP_VAULT=1 ;;
    --skip-storage) SKIP_STORAGE=1 ;;
    *) echo "Unknown option: $arg"; exit 1 ;;
  esac
done

TESTS_DIR="deployments/abc-nodes/nomad/tests"
PASS_JOBS=()
FAIL_JOBS=()
SKIP_JOBS=()

# ── helper: submit a batch job and wait for it to complete ────────────────────
run_test() {
  local label="$1"
  local hcl="$2"
  local job_name

  printf "\n  ▶  Running: %s\n" "$label"
  printf "     Job file: %s\n" "$hcl"
  job_name="$(basename "$hcl" .nomad.hcl)"

  # Submit and capture eval ID
  submit_out=$(abc admin services nomad cli -- job run -detach "$hcl" 2>&1)
  eval_id=$(printf "%s\n" "$submit_out" | awk '/Evaluation ID:/ { print $NF; exit }')

  if [ -z "$eval_id" ]; then
    printf "     ERROR: failed to submit job\n%s\n" "$submit_out"
    FAIL_JOBS+=("$label (submit failed)")
    return
  fi

  printf "     eval: %s\n" "$eval_id"

  # Wait up to 3 minutes for the allocation to complete
  local max_wait=180
  local waited=0
  local alloc_id=""
  local alloc_status=""

  while [ $waited -lt $max_wait ]; do
    sleep 4
    waited=$((waited + 4))

    # Resolve allocation ID from the latest allocation of this job.
    if [ -z "$alloc_id" ]; then
      alloc_id=$(abc admin services nomad cli -- job allocs -namespace services -json "$job_name" 2>/dev/null \
        | jq -r 'sort_by(.CreateTime) | reverse | .[0].ID // empty' 2>/dev/null || true)
    fi

    [ -z "$alloc_id" ] && continue

    alloc_status=$(abc admin services nomad cli -- alloc status -namespace services \
      -json "$alloc_id" 2>/dev/null \
      | jq -r '.ClientStatus // empty' 2>/dev/null || true)

    case "$alloc_status" in
      complete)
        printf "     status: complete (%ds)\n" "$waited"
        # Print the test output
        echo "     ─────────────────────────────────────────────────"
        abc admin services nomad cli -- alloc logs -namespace services "$alloc_id" test 2>/dev/null \
          | sed 's/^/     /'
        echo "     ─────────────────────────────────────────────────"
        PASS_JOBS+=("$label")
        return
        ;;
      failed)
        printf "     status: FAILED (%ds)\n" "$waited"
        echo "     ─────────────────────────────────────────────────"
        abc admin services nomad cli -- alloc logs -namespace services "$alloc_id" test 2>/dev/null \
          | sed 's/^/     /'
        echo "     ─────────────────────────────────────────────────"
        FAIL_JOBS+=("$label")
        return
        ;;
      running|pending|starting)
        printf "     waiting... (%ds / %ds)\r" "$waited" "$max_wait"
        ;;
      *)
        printf "     unknown status '%s' after %ds\n" "$alloc_status" "$waited"
        ;;
    esac
  done

  printf "\n     TIMEOUT after %ds — alloc %s status=%s\n" "$max_wait" "$alloc_id" "$alloc_status"
  FAIL_JOBS+=("$label (timeout)")
}

# ── main ──────────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║       abc-nodes test suite                           ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""
echo "  NOMAD_TOKEN: ${NOMAD_TOKEN:0:8}..."
echo ""

# Always run: connectivity
run_test "connectivity"      "$TESTS_DIR/connectivity.nomad.hcl"

# Always run: observability (no creds needed)
run_test "observability"     "$TESTS_DIR/observability.nomad.hcl"

# Always run: ntfy notifications (no creds needed)
run_test "notifications-ntfy" "$TESTS_DIR/notifications-ntfy.nomad.hcl"

# Optional: storage-minio (needs mc + creds)
if [ "$SKIP_STORAGE" -eq 0 ]; then
  run_test "storage-minio"   "$TESTS_DIR/storage-minio.nomad.hcl"
else
  printf "\n  ── Skipping: storage-minio (--skip-storage)\n"
  SKIP_JOBS+=("storage-minio")
fi

# Optional: auth-forwardauth (needs Nomad token)
if [ "$SKIP_AUTH" -eq 0 ]; then
  run_test "auth-forwardauth" "$TESTS_DIR/auth-forwardauth.nomad.hcl"
else
  printf "\n  ── Skipping: auth-forwardauth (--skip-auth)\n"
  SKIP_JOBS+=("auth-forwardauth")
fi

# Optional: vault (needs Vault unsealed + token)
if [ "$SKIP_VAULT" -eq 0 ]; then
  run_test "vault"           "$TESTS_DIR/vault.nomad.hcl"
else
  printf "\n  ── Skipping: vault (--skip-vault)\n"
  SKIP_JOBS+=("vault")
fi

# Optional: upload-tusd (needs Nomad token + mc)
if [ "$SKIP_UPLOAD" -eq 0 ]; then
  run_test "upload-tusd"     "$TESTS_DIR/upload-tusd.nomad.hcl"
else
  printf "\n  ── Skipping: upload-tusd (--skip-upload)\n"
  SKIP_JOBS+=("upload-tusd")
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║  Summary                                             ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""

for j in "${PASS_JOBS[@]+"${PASS_JOBS[@]}"}"; do
  printf "  ✓  %s\n" "$j"
done
for j in "${FAIL_JOBS[@]+"${FAIL_JOBS[@]}"}"; do
  printf "  ✗  %s\n" "$j"
done
for j in "${SKIP_JOBS[@]+"${SKIP_JOBS[@]}"}"; do
  printf "  -  %s  (skipped)\n" "$j"
done

echo ""
printf "  Passed: %d   Failed: %d   Skipped: %d\n" \
  "${#PASS_JOBS[@]}" "${#FAIL_JOBS[@]}" "${#SKIP_JOBS[@]}"
echo ""

[ "${#FAIL_JOBS[@]}" -eq 0 ] || exit 1
