#!/usr/bin/env bash
# validate-hcl-generation.sh
#
# Validates `abc job run` HCL generation locally — no cluster required.
# Checks that #ABC directives produce correct HCL output.
#
# Usage:
#   ./validate-hcl-generation.sh [--abc-bin <path>]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ABC_BIN="${ABC_BIN:-abc}"
PASS=0; FAIL=0

GREEN='\033[0;32m'; RED='\033[0;31m'; NC='\033[0m'
pass() { echo -e "${GREEN}  ✓${NC} $*"; ((PASS++)) || true; }
fail() { echo -e "${RED}  ✗${NC} $*"; ((FAIL++)) || true; }
header() { echo; echo "── $* ──"; }

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

# Helper: generate HCL from a script and check for a pattern.
check_hcl() {
  local label="$1" script="$2" pattern="$3"
  local out
  out=$("$ABC_BIN" job run "$script" 2>&1) || true
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label — pattern /$pattern/ not found"
    echo "    HCL output:"
    echo "$out" | head -30 | sed 's/^/    /'
  fi
}

# Helper: create a temp script with given preamble + body, check HCL.
check_directive() {
  local label="$1" directive="$2" pattern="$3"
  local tmp
  tmp=$(mktemp /tmp/abc-validate-XXXXXX.sh)
  cat > "$tmp" <<EOF
#!/bin/bash
$directive
echo "hello"
EOF
  local out
  out=$("$ABC_BIN" job run "$tmp" 2>&1) || true
  rm -f "$tmp"
  if echo "$out" | grep -qE "$pattern"; then
    pass "$label"
  else
    fail "$label — pattern /$pattern/ not found in HCL"
    echo "    HCL output:"
    echo "$out" | head -30 | sed 's/^/    /'
  fi
}

# ── Scheduler directives ──────────────────────────────────────────────────────
header "Scheduler directives → HCL"

check_directive \
  "--name produces job block" \
  '#ABC --name=my-test-job' \
  'job "my-test-job"'

check_directive \
  "--nodes produces count" \
  $'#ABC --name=x\n#ABC --nodes=5' \
  'count\s*=\s*5'

check_directive \
  "--cores produces resources.cores" \
  $'#ABC --name=x\n#ABC --cores=8' \
  'cores\s*=\s*8'

check_directive \
  "--mem=4G produces memory=4096" \
  $'#ABC --name=x\n#ABC --mem=4G' \
  'memory\s*=\s*4096'

check_directive \
  "--mem=512M produces memory=512" \
  $'#ABC --name=x\n#ABC --mem=512M' \
  'memory\s*=\s*512'

check_directive \
  "--gpus produces device block" \
  $'#ABC --name=x\n#ABC --gpus=2' \
  'device "nvidia/gpu"'

check_directive \
  "--driver=hpc-bridge sets driver" \
  $'#ABC --name=x\n#ABC --driver=hpc-bridge' \
  'driver\s*=\s*"hpc-bridge"'

check_directive \
  "--priority sets priority" \
  $'#ABC --name=x\n#ABC --priority=75' \
  'priority\s*=\s*75'

check_directive \
  "--dc sets datacenters" \
  $'#ABC --name=x\n#ABC --dc=za-cpt-dc1' \
  'datacenters\s*=\s*\["za-cpt-dc1"\]'

check_directive \
  "--region sets region" \
  $'#ABC --name=x\n#ABC --region=za-cpt' \
  'region\s*=\s*"za-cpt"'

check_directive \
  "--namespace sets namespace" \
  $'#ABC --name=x\n#ABC --namespace=research' \
  'namespace\s*=\s*"research"'

# ── Runtime exposure directives ───────────────────────────────────────────────
header "Runtime exposure directives → env block"

check_directive \
  "--alloc_id injects NOMAD_ALLOC_ID" \
  $'#ABC --name=x\n#ABC --alloc_id' \
  'NOMAD_ALLOC_ID'

check_directive \
  "--alloc_index injects NOMAD_ALLOC_INDEX" \
  $'#ABC --name=x\n#ABC --alloc_index' \
  'NOMAD_ALLOC_INDEX'

check_directive \
  "--cpu_cores injects NOMAD_CPU_CORES" \
  $'#ABC --name=x\n#ABC --cpu_cores' \
  'NOMAD_CPU_CORES'

check_directive \
  "--mem_limit injects NOMAD_MEMORY_LIMIT" \
  $'#ABC --name=x\n#ABC --mem_limit' \
  'NOMAD_MEMORY_LIMIT'

check_directive \
  "--task_dir injects NOMAD_TASK_DIR" \
  $'#ABC --name=x\n#ABC --task_dir' \
  'NOMAD_TASK_DIR'

check_directive \
  "--alloc_dir injects NOMAD_ALLOC_DIR" \
  $'#ABC --name=x\n#ABC --alloc_dir' \
  'NOMAD_ALLOC_DIR'

check_directive \
  "--dc (exposure) injects NOMAD_DC" \
  $'#ABC --name=x\n#ABC --dc' \
  'NOMAD_DC'

# ── Meta directives ───────────────────────────────────────────────────────────
header "Meta directives → meta block"

check_directive \
  "--meta produces meta block with key" \
  $'#ABC --name=x\n#ABC --meta=sample_id=S001' \
  'sample_id'

check_directive \
  "multiple --meta entries all appear" \
  $'#ABC --name=x\n#ABC --meta=k1=v1\n#ABC --meta=k2=v2' \
  'k1|k2'

# ── hello.sh fixture ──────────────────────────────────────────────────────────
header "hello.sh fixture"

if [[ -f "$SCRIPT_DIR/hello.sh" ]]; then
  check_hcl \
    "hello.sh generates valid job block" \
    "$SCRIPT_DIR/hello.sh" \
    'job "'

  check_hcl \
    "hello.sh uses raw_exec driver" \
    "$SCRIPT_DIR/hello.sh" \
    'driver\s*=\s*"raw_exec"'
fi

# ── test-array-job.sh fixture ─────────────────────────────────────────────────
header "test-array-job.sh fixture"

if [[ -f "$SCRIPT_DIR/test-array-job.sh" ]]; then
  check_hcl \
    "test-array-job generates count=3" \
    "$SCRIPT_DIR/test-array-job.sh" \
    'count\s*=\s*3'

  check_hcl \
    "test-array-job injects NOMAD_ALLOC_INDEX" \
    "$SCRIPT_DIR/test-array-job.sh" \
    'NOMAD_ALLOC_INDEX'

  check_hcl \
    "test-array-job has meta block" \
    "$SCRIPT_DIR/test-array-job.sh" \
    'validation_run'
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo
echo "────────────────────────────────"
echo -e "  ${GREEN}PASS${NC} $PASS   ${RED}FAIL${NC} $FAIL"
echo "────────────────────────────────"
[[ "$FAIL" -eq 0 ]]
