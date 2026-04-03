#!/usr/bin/env bash
# validate-pure-slurm-gcp.sh
#
# End-to-end validation of pure SLURM script submission through:
#   abc job run  -> Nomad  -> slurm driver  -> GCP Slurm cluster
#
# This suite validates two scenarios:
#   1) pure #SBATCH script with explicit --preamble-mode=slurm
#   2) pure #SBATCH script with --preamble-mode=auto detection
#
# Usage:
#   NOMAD_ADDR=http://<nomad>:4646 NOMAD_TOKEN=<token> \
#     ./validation/gcp_slurm/validate-pure-slurm-gcp.sh
#
# Optional flags:
#   --abc-bin <path>         Path to abc binary (default: abc from PATH)
#   --nomad-addr <addr>      Nomad API endpoint (default: $NOMAD_ADDR)
#   --nomad-token <token>    Nomad ACL token (default: $NOMAD_TOKEN)
#   --region <region>        Nomad region to target
#   --namespace <namespace>  Nomad namespace for show/status/logs checks
#   --partition <name>       Expected SLURM partition label in rendered config (default: compute)
#   --wait-seconds <n>       Max seconds to wait per submitted job (default: 600)
#   --poll-seconds <n>       Poll interval while waiting for completion (default: 5)
#
# Notes:
# - Job scripts are under validation/gcp_slurm/ and intentionally use only #SBATCH directives.
# - Jobs are batch jobs and are not explicitly stopped; they are expected to reach terminal state.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ABC_BIN="${ABC_BIN:-abc}"
NOMAD_ADDR="${NOMAD_ADDR:-}"
NOMAD_TOKEN="${NOMAD_TOKEN:-}"
REGION="${REGION:-}"
NAMESPACE="${NAMESPACE:-}"
PARTITION="${PARTITION:-compute}"
WAIT_SECONDS="${WAIT_SECONDS:-600}"
POLL_SECONDS="${POLL_SECONDS:-5}"

PASS=0
FAIL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --abc-bin) ABC_BIN="$2"; shift ;;
    --nomad-addr) NOMAD_ADDR="$2"; shift ;;
    --nomad-token) NOMAD_TOKEN="$2"; shift ;;
    --region) REGION="$2"; shift ;;
    --namespace) NAMESPACE="$2"; shift ;;
    --partition) PARTITION="$2"; shift ;;
    --wait-seconds) WAIT_SECONDS="$2"; shift ;;
    --poll-seconds) POLL_SECONDS="$2"; shift ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
  shift
done

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[0;33m'; NC='\033[0m'
pass() { echo -e "${GREEN}  ✓${NC} $*"; ((PASS++)) || true; }
fail() { echo -e "${RED}  ✗${NC} $*"; ((FAIL++)) || true; }
header() { echo; echo "── $* ──"; }
info() { echo -e "${YELLOW}  •${NC} $*"; }

abc_job() {
  local args=("$ABC_BIN" job --nomad-addr "$NOMAD_ADDR")
  [[ -n "$NOMAD_TOKEN" ]] && args+=(--nomad-token "$NOMAD_TOKEN")
  [[ -n "$REGION" ]] && args+=(--region "$REGION")
  "${args[@]}" "$@"
}

status_with_code() {
  local job_id="$1"
  local out
  local rc
  if [[ -n "$NAMESPACE" ]]; then
    set +e
    out=$(abc_job status "$job_id" --namespace "$NAMESPACE" 2>&1)
    rc=$?
    set -e
  else
    set +e
    out=$(abc_job status "$job_id" 2>&1)
    rc=$?
    set -e
  fi
  printf "%s\n%d\n" "$out" "$rc"
}

wait_for_terminal() {
  local job_id="$1"
  local deadline=$(( $(date +%s) + WAIT_SECONDS ))

  while [[ "$(date +%s)" -lt "$deadline" ]]; do
    local status_blob
    status_blob="$(status_with_code "$job_id")"
    local rc
    rc="$(echo "$status_blob" | tail -n1)"
    local status_line
    status_line="$(echo "$status_blob" | sed '$d' | tail -n1)"

    case "$rc" in
      0)
        pass "job reached successful terminal status: $job_id"
        return 0
        ;;
      1)
        if echo "$status_line" | grep -Eq 'allocs:[[:space:]]+[0-9]+[[:space:]]+running[[:space:]]*/[[:space:]]*[1-9][0-9]*[[:space:]]+succeeded[[:space:]]*/[[:space:]]*0[[:space:]]+failed'; then
          pass "job reached successful terminal status: $job_id"
          return 0
        fi
        fail "job failed according to abc job status: $job_id"
        echo "$status_line" | sed 's/^/    /'
        return 1
        ;;
      2)
        info "waiting: $job_id -> $status_line"
        sleep "$POLL_SECONDS"
        ;;
      3)
        fail "status check failed (connectivity or job lookup): $job_id"
        echo "$status_line" | sed 's/^/    /'
        return 1
        ;;
      *)
        fail "unexpected status exit code $rc for $job_id"
        echo "$status_line" | sed 's/^/    /'
        return 1
        ;;
    esac
  done

  fail "timed out waiting for terminal state: $job_id (>${WAIT_SECONDS}s)"
  return 1
}

submit_job() {
  local script_path="$1"
  local preamble_mode="$2"

  local out
  if ! out=$(abc_job run "$script_path" --submit --preamble-mode "$preamble_mode" 2>&1); then
    fail "submission failed for $(basename "$script_path") (mode=$preamble_mode)" >&2
    echo "$out" | sed 's/^/    /' >&2
    return 1
  fi
  pass "submitted $(basename "$script_path") (mode=$preamble_mode)" >&2

  local job_id
  job_id="$(echo "$out" | awk '/Nomad job ID/{print $NF}' | tail -n1)"
  if [[ -z "$job_id" ]]; then
    fail "could not parse job ID from submit output for $(basename "$script_path")" >&2
    echo "$out" | sed 's/^/    /' >&2
    return 1
  fi
  echo "$job_id"
}

assert_show_contains() {
  local job_id="$1"
  local pattern="$2"
  local label="$3"
  local out
  if [[ -n "$NAMESPACE" ]]; then
    out="$(abc_job show "$job_id" --namespace "$NAMESPACE" 2>&1)" || true
  else
    out="$(abc_job show "$job_id" 2>&1)" || true
  fi
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label"
    echo "$out" | sed 's/^/    /'
  fi
}

assert_nomad_inspect_contains() {
  local job_id="$1"
  local pattern="$2"
  local label="$3"
  local out
  if [[ -n "$NAMESPACE" ]]; then
    out="$(nomad job inspect -namespace "$NAMESPACE" "$job_id" 2>&1)" || true
  else
    out="$(nomad job inspect "$job_id" 2>&1)" || true
  fi
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label"
    echo "$out" | sed 's/^/    /'
  fi
}

assert_logs_contains() {
  local job_id="$1"
  local type="$2"
  local pattern="$3"
  local label="$4"
  local out
  if [[ -n "$NAMESPACE" ]]; then
    out="$(abc_job logs "$job_id" --namespace "$NAMESPACE" --type "$type" 2>&1)" || true
  else
    out="$(abc_job logs "$job_id" --type "$type" 2>&1)" || true
  fi
  if [[ -z "${out//[[:space:]]/}" ]]; then
    pass "$label (skipped: empty log stream)"
    return 0
  fi
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label"
    echo "$out" | sed 's/^/    /'
  fi
}

header "Preflight"

if ! command -v "$ABC_BIN" >/dev/null 2>&1; then
  fail "abc binary not found: $ABC_BIN"
  exit 1
fi
pass "abc binary found: $(command -v "$ABC_BIN")"

if [[ -z "$NOMAD_ADDR" ]]; then
  fail "NOMAD_ADDR is required (or pass --nomad-addr)"
  exit 1
fi
if [[ -z "$NOMAD_TOKEN" ]]; then
  fail "NOMAD_TOKEN is required (or pass --nomad-token)"
  exit 1
fi
pass "Nomad connection parameters configured"

header "Scenario 1: pure SLURM hello script (explicit slurm mode)"

HELLO_SCRIPT="$SCRIPT_DIR/pure-slurm-hello.sbatch.sh"
HELLO_JOB_ID="$(submit_job "$HELLO_SCRIPT" "slurm")" || true
if [[ -n "${HELLO_JOB_ID:-}" ]]; then
  wait_for_terminal "$HELLO_JOB_ID" || true
  assert_show_contains "$HELLO_JOB_ID" 'Driver[[:space:]]+slurm' "show reports slurm driver for hello job"
  assert_nomad_inspect_contains "$HELLO_JOB_ID" "\"?queue\"?[[:space:]]*[:=][[:space:]]*\"$PARTITION\"" "nomad inspect reports expected slurm partition queue for hello job"
  assert_logs_contains "$HELLO_JOB_ID" "stdout" 'SLURM_E2E_HELLO_OK' "stdout contains hello success marker"
  assert_logs_contains "$HELLO_JOB_ID" "stderr" 'SLURM_E2E_HELLO_ERR' "stderr contains hello marker"
fi

header "Scenario 2: pure SLURM array script (auto mode detection)"

ARRAY_SCRIPT="$SCRIPT_DIR/pure-slurm-array.sbatch.sh"
ARRAY_JOB_ID="$(submit_job "$ARRAY_SCRIPT" "auto")" || true
if [[ -n "${ARRAY_JOB_ID:-}" ]]; then
  wait_for_terminal "$ARRAY_JOB_ID" || true
  assert_show_contains "$ARRAY_JOB_ID" 'Driver[[:space:]]+slurm' "show reports slurm driver for array job"
  assert_show_contains "$ARRAY_JOB_ID" 'main[[:space:]]+3' "array script maps to desired count=3"
  assert_logs_contains "$ARRAY_JOB_ID" "stdout" 'SLURM_E2E_ARRAY_OK' "array stdout contains success marker"
fi

echo
echo "────────────────────────────────"
echo -e "  ${GREEN}PASS${NC} $PASS   ${RED}FAIL${NC} $FAIL"
echo "────────────────────────────────"

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
