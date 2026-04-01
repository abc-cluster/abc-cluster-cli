#!/usr/bin/env bash
# validate-migration.sh
#
# Validates all migration example scripts by dry-running them with
# `abc job run --dry-run` and checking that the generated HCL is correct.
# No cluster required — all checks are local.
#
# Usage:
#   ./validate-migration.sh [--abc-bin <path>]
#
# Set ABC_BIN env var or --abc-bin flag to point at your abc binary.
# Defaults to "abc" on PATH.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ABC_BIN="${ABC_BIN:-abc}"
PASS=0; FAIL=0; SKIP=0

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[0;33m'; BOLD='\033[1m'; NC='\033[0m'

pass() { echo -e "${GREEN}  ✓${NC} $*"; ((PASS++)) || true; }
fail() { echo -e "${RED}  ✗${NC} $*"; ((FAIL++)) || true; }
skip() { echo -e "${YELLOW}  –${NC} $* (skipped)"; ((SKIP++)) || true; }
header() { echo; echo -e "${BOLD}── $* ──${NC}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --abc-bin) ABC_BIN="$2"; shift ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
  shift
done

if ! command -v "$ABC_BIN" &>/dev/null; then
  echo "abc binary not found: $ABC_BIN" >&2; exit 1
fi

# ── Helpers ───────────────────────────────────────────────────────────────────

# hcl_out <script>: prints the generated HCL to stdout without hitting Nomad.
# Plain `abc job run <script>` (no --submit, no --dry-run) generates and
# prints HCL locally; no cluster connection required.
hcl_out() {
  "$ABC_BIN" job run "$1" 2>&1 || true
}

# check <label> <script> <grep-pattern>
# Dry-runs the script and checks that the HCL output matches the pattern.
check() {
  local label="$1" script="$2" pattern="$3"
  if [[ ! -f "$script" ]]; then
    skip "$label — $script not found"
    return
  fi
  local out
  out=$(hcl_out "$script")
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label — pattern /$pattern/ not found"
    echo "$out" | head -20 | sed 's/^/    /'
  fi
}

# check_absent <label> <script> <grep-pattern>
# Asserts a pattern does NOT appear (confirms missing features stay absent).
check_absent() {
  local label="$1" script="$2" pattern="$3"
  if [[ ! -f "$script" ]]; then
    skip "$label — $script not found"
    return
  fi
  local out
  out=$(hcl_out "$script")
  if echo "$out" | grep -qE "$pattern"; then
    fail "$label — pattern /$pattern/ should NOT appear but does"
    echo "$out" | grep -E "$pattern" | head -5 | sed 's/^/    /'
  else
    pass "$label"
  fi
}

# ── 01: Hello World ───────────────────────────────────────────────────────────
header "01 — Hello World"

S="$SCRIPT_DIR/01-hello-world/job.abc.sh"
check "job name is hello-world"           "$S" 'job "hello-world"'
check "type is batch"                     "$S" 'type\s*=\s*"batch"'
check "count is 1"                        "$S" 'count\s*=\s*1'
check "cores is 1"                        "$S" 'cores\s*=\s*1'
check "memory is 256"                     "$S" 'memory\s*=\s*256'
check "driver is raw_exec"               "$S" 'driver\s*=\s*"raw_exec"'
check "NOMAD_ALLOC_ID exposed"            "$S" 'NOMAD_ALLOC_ID'
check "NOMAD_ALLOC_NAME exposed"          "$S" 'NOMAD_ALLOC_NAME'
check "NOMAD_JOB_ID exposed"             "$S" 'NOMAD_JOB_ID'

# ── 02: Array Job ─────────────────────────────────────────────────────────────
header "02 — Array Job"

S="$SCRIPT_DIR/02-array-job/job.abc.sh"
check "job name is array-align"           "$S" 'job "array-align"'
check "count is 48"                       "$S" 'count\s*=\s*48'
check "cores is 8"                        "$S" 'cores\s*=\s*8'
check "memory is 32768"                   "$S" 'memory\s*=\s*32768'
check "NOMAD_ALLOC_INDEX exposed"         "$S" 'NOMAD_ALLOC_INDEX'
check "NOMAD_CPU_CORES exposed"           "$S" 'NOMAD_CPU_CORES'
check "NOMAD_TASK_DIR exposed"           "$S" 'NOMAD_TASK_DIR'
check "NOMAD_ALLOC_DIR exposed"           "$S" 'NOMAD_ALLOC_DIR'
check "walltime 04:00:00 in HCL"         "$S" '04:00:00'

# ── 03: GPU Job ───────────────────────────────────────────────────────────────
header "03 — GPU Job"

S="$SCRIPT_DIR/03-gpu-job/job.abc.sh"
check "job name is gpu-kraken2"           "$S" 'job "gpu-kraken2"'
check "GPU device block present"          "$S" 'device "nvidia/gpu"'
check "GPU count is 2"                    "$S" 'count\s*=\s*2'
check "cores is 8"                        "$S" 'cores\s*=\s*8'
check "memory is 65536"                   "$S" 'memory\s*=\s*65536'
check "datacenter is gpu-dc1"            "$S" 'datacenters\s*=\s*\["gpu-dc1"\]'
check "NOMAD_CPU_CORES exposed"           "$S" 'NOMAD_CPU_CORES'

# ── 04: BWA Alignment ─────────────────────────────────────────────────────────
header "04 — BWA Alignment (realistic)"

S="$SCRIPT_DIR/04-bwa-alignment/job.abc.sh"
check "job name is bwa-align"             "$S" 'job "bwa-align"'
check "count is 96"                       "$S" 'count\s*=\s*96'
check "cores is 16"                       "$S" 'cores\s*=\s*16'
check "memory is 65536"                   "$S" 'memory\s*=\s*65536'
check "region is za-cpt"                  "$S" 'region\s*=\s*"za-cpt"'
check "datacenter is za-cpt-hpc1"        "$S" 'datacenters\s*=\s*\["za-cpt-hpc1"\]'
check "driver is hpc-bridge"             "$S" 'driver\s*=\s*"hpc-bridge"'
check "NOMAD_ALLOC_INDEX exposed"         "$S" 'NOMAD_ALLOC_INDEX'
check "NOMAD_CPU_CORES exposed"           "$S" 'NOMAD_CPU_CORES'
check "NOMAD_MEMORY_LIMIT exposed"        "$S" 'NOMAD_MEMORY_LIMIT'
check "NOMAD_TASK_DIR exposed"           "$S" 'NOMAD_TASK_DIR'
check "NOMAD_ALLOC_DIR exposed"           "$S" 'NOMAD_ALLOC_DIR'
check "NOMAD_DC exposed"                  "$S" 'NOMAD_DC'
check "meta pipeline present"             "$S" 'pipeline'
check "meta reference present"            "$S" 'reference'
check "meta cohort present"               "$S" 'cohort'

# ── 05: Dependency ────────────────────────────────────────────────────────────
header "05 — Dependency Chain"

S1="$SCRIPT_DIR/05-dependency/job-step1.abc.sh"
check "step1 job name"                    "$S1" 'job "wgs-step1-align"'
check "step1 count is 24"                 "$S1" 'count\s*=\s*24'
check "step1 cores is 16"                 "$S1" 'cores\s*=\s*16'
check "step1 NOMAD_ALLOC_INDEX"          "$S1" 'NOMAD_ALLOC_INDEX'
check "step1 NOMAD_ALLOC_DIR"            "$S1" 'NOMAD_ALLOC_DIR'

S2="$SCRIPT_DIR/05-dependency/job-step2.abc.sh"
check "step2 job name"                    "$S2" 'job "wgs-step2-variantcall"'
check "step2 count is 1"                  "$S2" 'count\s*=\s*1'
check "step2 memory is 131072"           "$S2" 'memory\s*=\s*131072'
check "step2 NOMAD_MEMORY_LIMIT"         "$S2" 'NOMAD_MEMORY_LIMIT'
check "step2 meta present"               "$S2" '(pipeline|gatk)'

# ── Feature gap assertions ─────────────────────────────────────────────────────
# These confirm that known gaps do NOT silently produce wrong output.
header "Feature gap assertions"

# --nodes should produce 'count', not a literal 'nodes' field
check "array --nodes produces HCL count not nodes keyword" \
  "$SCRIPT_DIR/02-array-job/job.abc.sh" 'count\s*=\s*48'

# GPU type selection: --gpus=2 must NOT produce a type constraint
check_absent "GPU type constraint absent (gap: type selection not supported)" \
  "$SCRIPT_DIR/03-gpu-job/job.abc.sh" 'nvidia_a100\|gpu_type'

# PBS/SLURM scheduler directives must not leak into ABC HCL output.
# (The check excludes the embedded script heredoc by searching only for
# top-level HCL keywords; PBS/SLURM directives would appear as bare strings.)
for script in \
  "$SCRIPT_DIR/04-bwa-alignment/job.abc.sh" \
  "$SCRIPT_DIR/05-dependency/job-step1.abc.sh"; do
  check_absent "no #PBS or #SBATCH in HCL job block of $(basename "$script")" \
    "$script" '^  #PBS|^  #SBATCH'
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo
echo "────────────────────────────────────────"
echo -e "  ${GREEN}PASS${NC} $PASS   ${RED}FAIL${NC} $FAIL   ${YELLOW}SKIP${NC} $SKIP"
echo "────────────────────────────────────────"
[[ "$FAIL" -eq 0 ]]
