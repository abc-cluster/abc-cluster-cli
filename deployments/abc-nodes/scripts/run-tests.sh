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

# `jq` is commonly installed to /usr/local/bin or /opt/homebrew/bin, which may
# not be on PATH for non-interactive runners (CI/agents). Resolve explicitly.
JQ_BIN="$(command -v jq 2>/dev/null || true)"
if [ -z "$JQ_BIN" ]; then
  for candidate in /opt/homebrew/bin/jq /usr/local/bin/jq; do
    if [ -x "$candidate" ]; then
      JQ_BIN="$candidate"
      break
    fi
  done
fi
if [ -z "$JQ_BIN" ]; then
  echo "ERROR: jq is required to parse Nomad JSON output, but it was not found on PATH." >&2
  echo "Install jq or ensure a standard location is available (/opt/homebrew/bin/jq or /usr/local/bin/jq)." >&2
  exit 127
fi

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
  local job_ns="services"

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
  local start_ts="$SECONDS"
  local waited=0
  local alloc_id=""
  local alloc_status=""
  local sleep_s=0

  resolve_latest_alloc_id() {
    # Prefer `job allocs` (small payload), but fall back to `job status -json`
    # because very fast batch jobs can GC allocations from the allocs index
    # before slower polling loops observe them.
    local from_allocs from_status
    from_allocs=$(abc admin services nomad cli -- job allocs -namespace "$job_ns" -json "$job_name" 2>/dev/null \
      | "$JQ_BIN" -r 'sort_by(.CreateTime) | reverse | .[0].ID // empty' 2>/dev/null || true)
    if [ -n "$from_allocs" ]; then
      printf "%s" "$from_allocs"
      return 0
    fi

    from_status=$(abc admin services nomad cli -- job status -namespace "$job_ns" -json "$job_name" 2>/dev/null \
      | "$JQ_BIN" -r '.Allocations // [] | sort_by(.CreateTime) | reverse | .[0].ID // empty' 2>/dev/null || true)
    printf "%s" "$from_status"
  }

  while [ "$waited" -lt "$max_wait" ]; do
    # Resolve allocation ID from the latest allocation of this job.
    if [ -z "$alloc_id" ]; then
      alloc_id="$(resolve_latest_alloc_id)"
    fi

    if [ -z "$alloc_id" ]; then
      # Tight loop early: batch tests often finish in <2s; sleeping 4s first
      # can miss the alloc entirely if GC is aggressive.
      if [ "$waited" -lt 3 ]; then
        sleep_s="0.1"
      elif [ "$waited" -lt 15 ]; then
        sleep_s="0.25"
      elif [ "$waited" -lt 45 ]; then
        sleep_s=1
      else
        sleep_s=2
      fi
    else
      sleep_s=1
    fi

    # If we already have an alloc, check status immediately (batch jobs can
    # finish before we'd otherwise sleep).
    if [ -n "$alloc_id" ]; then
      alloc_status=$(abc admin services nomad cli -- alloc status -namespace "$job_ns" \
        -json "$alloc_id" 2>/dev/null \
        | "$JQ_BIN" -r '.ClientStatus // empty' 2>/dev/null || true)

      case "$alloc_status" in
        complete)
          printf "     status: complete (%ds)\n" "$waited"
          echo "     ─────────────────────────────────────────────────"
          abc admin services nomad cli -- alloc logs -namespace "$job_ns" "$alloc_id" test 2>/dev/null \
            | sed 's/^/     /'
          echo "     ─────────────────────────────────────────────────"
          PASS_JOBS+=("$label")
          return
          ;;
        failed|lost)
          printf "     status: FAILED (%ds)\n" "$waited"
          echo "     ─────────────────────────────────────────────────"
          abc admin services nomad cli -- alloc logs -namespace "$job_ns" "$alloc_id" test 2>/dev/null \
            | sed 's/^/     /'
          echo "     ─────────────────────────────────────────────────"
          FAIL_JOBS+=("$label")
          return
          ;;
      esac
    fi

    remaining=$((max_wait - waited))
    [ "$remaining" -le 0 ] && break

    # Bash $((..)) is integer-only; keep sub-second sleeps, but clamp using a
    # crude ceiling in whole seconds for the remaining budget.
    if [ "$sleep_s" != "1" ] && [ "$sleep_s" != "2" ]; then
      if [ "$remaining" -lt 1 ]; then
        sleep_s="0.1"
      fi
    else
      if [ "$sleep_s" -gt "$remaining" ]; then
        sleep_s="$remaining"
      fi
    fi

    sleep "$sleep_s"
    waited=$((SECONDS - start_ts))

    [ -z "$alloc_id" ] && continue

    alloc_status=$(abc admin services nomad cli -- alloc status -namespace "$job_ns" \
      -json "$alloc_id" 2>/dev/null \
      | "$JQ_BIN" -r '.ClientStatus // empty' 2>/dev/null || true)

    case "$alloc_status" in
      complete)
        printf "     status: complete (%ds)\n" "$waited"
        # Print the test output
        echo "     ─────────────────────────────────────────────────"
        abc admin services nomad cli -- alloc logs -namespace "$job_ns" "$alloc_id" test 2>/dev/null \
          | sed 's/^/     /'
        echo "     ─────────────────────────────────────────────────"
        PASS_JOBS+=("$label")
        return
        ;;
      failed|lost)
        printf "     status: FAILED (%ds)\n" "$waited"
        echo "     ─────────────────────────────────────────────────"
        abc admin services nomad cli -- alloc logs -namespace "$job_ns" "$alloc_id" test 2>/dev/null \
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

  waited=$((SECONDS - start_ts))
  printf "\n     TIMEOUT after %ds — alloc %s status=%s\n" "$waited" "$alloc_id" "$alloc_status"
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
  run_test "vault"           "deployments/abc-nodes/experimental/nomad/tests/vault.nomad.hcl"
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
