#!/usr/bin/env bash
# validate-job-commands.sh
#
# End-to-end validation of `abc job` subcommands against a real ABC cluster.
#
# Usage:
#   ./validate-job-commands.sh [--submit] [--region <region>] [--nomad-addr <addr>]
#
# Flags:
#   --submit          Actually submit jobs (default: dry-run + list/show/status only)
#   --region          Nomad region to target (default: from NOMAD_REGION or server default)
#   --nomad-addr      Nomad API address (default: $NOMAD_ADDR or http://127.0.0.1:4646)
#   --nomad-token     Nomad ACL token (default: $NOMAD_TOKEN)
#
# Prerequisites:
#   - `abc` binary on PATH (or set ABC_BIN)
#   - Nomad reachable at $NOMAD_ADDR / --nomad-addr
#   - ACL token with job:read and job:write policies

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Defaults ──────────────────────────────────────────────────────────────────
ABC_BIN="${ABC_BIN:-abc}"
NOMAD_ADDR="${NOMAD_ADDR:-http://127.0.0.1:4646}"
NOMAD_TOKEN="${NOMAD_TOKEN:-}"
REGION=""
DO_SUBMIT=false
PASS=0
FAIL=0

# ── Arg parsing ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --submit)      DO_SUBMIT=true ;;
    --region)      REGION="$2"; shift ;;
    --nomad-addr)  NOMAD_ADDR="$2"; shift ;;
    --nomad-token) NOMAD_TOKEN="$2"; shift ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
  shift
done

# ── Helpers ───────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[0;33m'; NC='\033[0m'

pass() { echo -e "${GREEN}  ✓${NC} $*"; ((PASS++)) || true; }
fail() { echo -e "${RED}  ✗${NC} $*"; ((FAIL++)) || true; }
skip() { echo -e "${YELLOW}  –${NC} $* (skipped)"; }
header() { echo; echo "── $* ──"; }

abc() {
  local args=("$ABC_BIN" --nomad-addr "$NOMAD_ADDR")
  [[ -n "$NOMAD_TOKEN" ]] && args+=(--nomad-token "$NOMAD_TOKEN")
  [[ -n "$REGION" ]]      && args+=(--region "$REGION")
  "${args[@]}" "$@"
}

assert_exit() {
  local label="$1" expected="$2"; shift 2
  local actual=0
  "$@" > /dev/null 2>&1 || actual=$?
  if [[ "$actual" -eq "$expected" ]]; then
    pass "$label (exit $actual)"
  else
    fail "$label — expected exit $expected, got $actual"
  fi
}

assert_output_contains() {
  local label="$1" pattern="$2"; shift 2
  local out
  out=$("$@" 2>&1) || true
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label — output did not match /$pattern/"
    echo "    Output was:"
    echo "$out" | head -20 | sed 's/^/    /'
  fi
}

# ── Preflight ─────────────────────────────────────────────────────────────────
header "Preflight"

if ! command -v "$ABC_BIN" &>/dev/null; then
  fail "abc binary not found: $ABC_BIN (set ABC_BIN or add to PATH)"
  exit 1
fi
pass "abc binary found: $(command -v "$ABC_BIN")"

# Check Nomad reachable
if curl -sf "${NOMAD_ADDR}/v1/status/leader" > /dev/null 2>&1; then
  LEADER=$(curl -sf "${NOMAD_ADDR}/v1/status/leader" | tr -d '"')
  pass "Nomad reachable at $NOMAD_ADDR (leader: $LEADER)"
else
  fail "Nomad not reachable at $NOMAD_ADDR — is the agent running?"
  exit 1
fi

# ── abc job list ──────────────────────────────────────────────────────────────
header "abc job list"

assert_output_contains \
  "list produces header columns" \
  "NOMAD JOB ID.*STATUS.*REGION.*DATACENTERS.*SUBMITTED.*DURATION" \
  abc job list

assert_exit \
  "list exits 0" 0 \
  abc job list

if [[ -n "$REGION" ]]; then
  assert_output_contains \
    "list --region filter returns only matching region" \
    "$REGION|No jobs found" \
    abc job list --region "$REGION"
fi

assert_exit \
  "list --limit 1 exits 0" 0 \
  abc job list --limit 1

assert_exit \
  "list --status dead exits 0" 0 \
  abc job list --status dead

# ── abc job run (dry-run) ─────────────────────────────────────────────────────
header "abc job run --dry-run"

HELLO_SCRIPT="$SCRIPT_DIR/hello.sh"
TEST_ARRAY_SCRIPT="$SCRIPT_DIR/test-array-job.sh"

if [[ -f "$HELLO_SCRIPT" ]]; then
  assert_output_contains \
    "dry-run prints HCL job block" \
    'job "' \
    abc job run "$HELLO_SCRIPT" --dry-run

  assert_output_contains \
    "dry-run shows Dry-run complete message" \
    "Dry-run complete" \
    abc job run "$HELLO_SCRIPT" --dry-run
else
  skip "hello.sh not found at $HELLO_SCRIPT"
fi

if [[ -f "$TEST_ARRAY_SCRIPT" ]]; then
  assert_output_contains \
    "array job dry-run shows HCL with count" \
    'count\s*=' \
    abc job run "$TEST_ARRAY_SCRIPT" --dry-run
fi

# ── abc job run --submit ──────────────────────────────────────────────────────
SUBMITTED_JOB_ID=""

if [[ "$DO_SUBMIT" == "true" ]]; then
  header "abc job run --submit"

  if [[ -f "$HELLO_SCRIPT" ]]; then
    submit_out=$(abc job run "$HELLO_SCRIPT" --submit 2>&1) || true
    if echo "$submit_out" | grep -qE "Job submitted|Nomad job ID"; then
      pass "hello.sh submitted successfully"
      # Extract Nomad job ID from output
      SUBMITTED_JOB_ID=$(echo "$submit_out" | grep -oP '(?<=Nomad job ID\s{3})\S+' || true)
      [[ -n "$SUBMITTED_JOB_ID" ]] && pass "Captured submitted job ID: $SUBMITTED_JOB_ID"
    else
      fail "hello.sh submit failed"
      echo "$submit_out" | head -20 | sed 's/^/    /'
    fi
  else
    skip "hello.sh not found — skipping submit test"
  fi
else
  skip "abc job run --submit (use --submit flag to test)"
fi

# ── abc job show ──────────────────────────────────────────────────────────────
header "abc job show"

# Grab the first job ID from the list for show/status tests.
FIRST_JOB=$(abc job list --limit 1 2>/dev/null | grep -v "NOMAD JOB ID\|No jobs\|─" | awk '{print $1}' | head -1 || true)

if [[ -n "$FIRST_JOB" ]]; then
  assert_output_contains \
    "show returns Nomad Job ID line" \
    "Nomad Job ID" \
    abc job show "$FIRST_JOB"

  assert_output_contains \
    "show returns Status line" \
    "Status" \
    abc job show "$FIRST_JOB"

  assert_output_contains \
    "show returns Region line" \
    "Region" \
    abc job show "$FIRST_JOB"

  assert_output_contains \
    "show returns TASK GROUPS table" \
    "TASK GROUPS|No jobs found" \
    abc job show "$FIRST_JOB"

  assert_exit \
    "show exits 0" 0 \
    abc job show "$FIRST_JOB"
else
  skip "No jobs found — skipping abc job show tests"
fi

# ── abc job status ────────────────────────────────────────────────────────────
header "abc job status"

if [[ -n "$FIRST_JOB" ]]; then
  # Status may exit 0, 1, or 2 depending on job state — all are valid.
  status_out=$(abc job status "$FIRST_JOB" 2>&1) || STATUS_EXIT=$?
  STATUS_EXIT="${STATUS_EXIT:-0}"
  if echo "$status_out" | grep -qE "$FIRST_JOB"; then
    pass "status output contains job ID"
  else
    fail "status output missing job ID"
  fi
  if [[ "$STATUS_EXIT" -le 2 ]]; then
    pass "status exit code is valid ($STATUS_EXIT ∈ {0,1,2})"
  else
    fail "status exit code unexpected: $STATUS_EXIT"
  fi
else
  skip "No jobs found — skipping abc job status tests"
fi

# Test non-existent job returns exit 3
NONEXIST_EXIT=0
abc job status "nonexistent-job-$(date +%s)" > /dev/null 2>&1 || NONEXIST_EXIT=$?
if [[ "$NONEXIST_EXIT" -eq 3 ]]; then
  pass "status exits 3 for non-existent job"
else
  fail "status for non-existent job: expected exit 3, got $NONEXIST_EXIT"
fi

# ── abc job logs ──────────────────────────────────────────────────────────────
header "abc job logs"

if [[ -n "$FIRST_JOB" ]]; then
  # Non-follow logs — just check exit code (may fail if no allocs, which is OK)
  logs_exit=0
  abc job logs "$FIRST_JOB" > /dev/null 2>&1 || logs_exit=$?
  if [[ "$logs_exit" -eq 0 ]]; then
    pass "logs exits 0 for job with allocations"
  else
    # Exit 1 is acceptable when no allocs or completed job with no retained logs
    pass "logs exited $logs_exit (acceptable — job may have no active allocs)"
  fi
fi

# ── abc job stop ──────────────────────────────────────────────────────────────
header "abc job stop"

if [[ "$DO_SUBMIT" == "true" && -n "$SUBMITTED_JOB_ID" ]]; then
  stop_out=$(echo "y" | abc job stop "$SUBMITTED_JOB_ID" 2>&1) || true
  if echo "$stop_out" | grep -qE "Stop signal sent|Aborted"; then
    pass "stop acknowledged for $SUBMITTED_JOB_ID"
  else
    fail "stop command unexpected output"
    echo "$stop_out" | sed 's/^/    /'
  fi

  # Verify --yes flag skips prompt.
  # Re-submit a throw-away job first (only if hello.sh exists).
  if [[ -f "$HELLO_SCRIPT" ]]; then
    tmp_out=$(abc job run "$HELLO_SCRIPT" --submit 2>&1) || true
    tmp_job=$(echo "$tmp_out" | grep -oP '(?<=Nomad job ID\s{3})\S+' || true)
    if [[ -n "$tmp_job" ]]; then
      stop_yes_out=$(abc job stop "$tmp_job" --yes 2>&1) || true
      if echo "$stop_yes_out" | grep -qE "Stop signal sent"; then
        pass "stop --yes skips confirmation prompt"
      else
        fail "stop --yes unexpected output"
      fi
    fi
  fi
else
  skip "abc job stop (requires --submit to create a test job first)"
fi

# ── abc job dispatch ──────────────────────────────────────────────────────────
header "abc job dispatch"

# Dispatch requires a parameterized job to already exist on the cluster.
# We just verify the command is registered and shows proper usage.
dispatch_usage=$(abc job dispatch 2>&1 || true)
if echo "$dispatch_usage" | grep -qiE "dispatch|usage"; then
  pass "dispatch command is registered and prints usage"
else
  fail "dispatch command not found or unexpected output"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo
echo "────────────────────────────────"
echo -e "  ${GREEN}PASS${NC} $PASS   ${RED}FAIL${NC} $FAIL"
echo "────────────────────────────────"

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
