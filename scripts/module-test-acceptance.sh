#!/usr/bin/env bash
# module-test-acceptance.sh — submit `abc module run --test` for every
# bundled-test submodule of selected nf-core toolkits in parallel, wait for
# the cluster to drain, and print a per-tool pass/fail summary.
#
# Defaults: samtools only. Add other toolkits with --enable a,b,c or --all.
#
# Usage:
#   ./module-test-acceptance.sh                              # samtools only
#   ./module-test-acceptance.sh --enable gatk4,bcftools      # + selections
#   ./module-test-acceptance.sh --all                        # full sweep
#   ./module-test-acceptance.sh --tools                      # list known tools
#
# Required env (or pass via flags):
#   NOMAD_ADDR / NOMAD_TOKEN     Nomad API
#   GITHUB_TOKEN                 used by prestart for nf-core/modules tarball
#                                (avoids GitHub anonymous rate-limit)
#
# This script does NOT install/build anything; it expects:
#   - `abc` binary on PATH (or pass --abc PATH)
#   - nf-core/modules checkout under MODULES_DIR (default: /tmp/pgtest/modules-src)
#     Used only to enumerate submodules with bundled tests.
#   - nf-pipeline-gen JAR mirrored on rustfs at
#     ${RUSTFS_BASE}/releases/nf-pipeline-gen/${PG_VERSION}/pipeline-gen.jar
set -uo pipefail

# ── known toolkits, sorted alphabetically (samtools is the default) ───────────
# Parallel arrays instead of associative arrays so this runs on macOS bash 3.2.
ALL_TOOLS_ORDERED=(samtools gatk4 bcftools picard bedtools seqkit plink mmseqs parabricks bbmap)
ALL_TOOL_COUNTS=(35       64    28      27     23       17     13    13     12         11)
DEFAULT_TOOLS="samtools"

# Toolkits that require special hardware / drivers and are EXCLUDED from
# `--all` unless explicitly named via `--enable parabricks` / `--only parabricks`.
# parabricks needs CUDA GPUs; none of our docker-driver Nomad nodes have them
# yet, so leaving it in --all just produces noise (10/12 modules fail with
# missing-CUDA / OOM signatures).
SPECIAL_TOOLS=" parabricks "

is-special() { case "$SPECIAL_TOOLS" in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

tool_count() {
  local t="$1" i=0
  for n in "${ALL_TOOLS_ORDERED[@]}"; do
    if [ "$n" = "$t" ]; then echo "${ALL_TOOL_COUNTS[$i]}"; return; fi
    i=$((i+1))
  done
  echo ""
}

# ── defaults ──────────────────────────────────────────────────────────────────
ABC_BIN="${ABC_BIN:-${HOME}/bin/abc}"
[ -x "$ABC_BIN" ] || ABC_BIN=$(command -v abc 2>/dev/null || echo /tmp/abc)
MODULES_DIR="${MODULES_DIR:-/tmp/pgtest/modules-src}"
RUSTFS_BASE="${RUSTFS_BASE:-http://100.70.185.46:9900}"
PG_VERSION="${PG_VERSION:-v0.1.0-dev7}"
S3_ACCESS_KEY="${S3_ACCESS_KEY:-rustfsadmin}"
S3_SECRET_KEY="${S3_SECRET_KEY:-rustfsadmin}"
DCS=("sun-nomadlab" "gcp-nomadlab" "oci-nomadlab" "default")
DCS_OVERRIDDEN=0
HOST_VOLUME=""
TASK_DRIVER=""
WAIT_MIN=15
PARALLEL=8
ENABLE=""
ONLY=""
USE_ALL=0
DRY_RUN=0
RESULTS_DIR="${RESULTS_DIR:-/tmp/module-acceptance-$(date +%Y%m%dT%H%M%S)}"

usage() {
  cat <<EOF
Usage: $0 [options]

Selection:
  --enable a,b,c    Comma-separated extra toolkits ADDED to default (samtools)
  --only a,b,c      Comma-separated toolkits REPLACING the default (no samtools)
  --all             Enable every known toolkit
  --tools           Print known toolkits with submodule counts and exit

Submission:
  --abc PATH        Path to abc binary (default: \$ABC_BIN)
  --modules-dir P   nf-core/modules checkout (default: \$MODULES_DIR)
  --rustfs-base U   RustFS S3 base URL (default: \$RUSTFS_BASE)
  --pg-version V    nf-pipeline-gen version on RustFS (default: \$PG_VERSION)
  --datacenter D    Repeatable; overrides default DC list
  --parallel N      xargs -P (default $PARALLEL)
  --wait MIN        Wait minutes for cluster to drain (default $WAIT_MIN)
  --dry-run         Enumerate selected modules and exit without submitting

Output:
  --results-dir D   Where to write per-run TSVs (default: \$RESULTS_DIR)

Required env: NOMAD_ADDR, NOMAD_TOKEN, GITHUB_TOKEN

Exit code: 0 if all selected toolkits had >= 50% pass rate, 1 otherwise.
EOF
}

list_tools() {
  printf "%-12s %5s\n" "TOOLKIT" "MODS"
  local i=0
  for t in "${ALL_TOOLS_ORDERED[@]}"; do
    marker=" "
    [ "$t" = "samtools" ] && marker="*"
    if is-special "$t"; then marker="!"; fi
    printf "%s %-10s %5d\n" "$marker" "$t" "${ALL_TOOL_COUNTS[$i]}"
    i=$((i+1))
  done
  echo
  echo "* = enabled by default"
  echo "! = excluded from --all (special hardware required); enable explicitly with --enable / --only"
}

# ── arg parsing ───────────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
  case "$1" in
    --enable)        ENABLE="$2"; shift 2 ;;
    --only)          ONLY="$2"; shift 2 ;;
    --all)           USE_ALL=1; shift ;;
    --tools)         list_tools; exit 0 ;;
    --abc)           ABC_BIN="$2"; shift 2 ;;
    --modules-dir)   MODULES_DIR="$2"; shift 2 ;;
    --rustfs-base)   RUSTFS_BASE="$2"; shift 2 ;;
    --pg-version)    PG_VERSION="$2"; shift 2 ;;
    --datacenter)
      # First --datacenter clears the defaults; subsequent ones append.
      if [ "$DCS_OVERRIDDEN" = "0" ]; then DCS=(); DCS_OVERRIDDEN=1; fi
      DCS+=("$2"); shift 2
      ;;
    --host-volume)   HOST_VOLUME="$2"; shift 2 ;;
    --driver)        TASK_DRIVER="$2"; shift 2 ;;
    --parallel)      PARALLEL="$2"; shift 2 ;;
    --wait)          WAIT_MIN="$2"; shift 2 ;;
    --dry-run)       DRY_RUN=1; shift ;;
    --results-dir)   RESULTS_DIR="$2"; shift 2 ;;
    -h|--help)       usage; exit 0 ;;
    *)               echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
done

# ── env validation ────────────────────────────────────────────────────────────
: "${NOMAD_ADDR:?NOMAD_ADDR not set}"
: "${NOMAD_TOKEN:?NOMAD_TOKEN not set}"
: "${GITHUB_TOKEN:?GITHUB_TOKEN not set (export GITHUB_TOKEN=\$(gh auth token))}"
[ -x "$ABC_BIN" ] || { echo "abc binary not executable: $ABC_BIN" >&2; exit 2; }
[ -d "$MODULES_DIR/modules/nf-core" ] || { echo "modules dir missing: $MODULES_DIR/modules/nf-core" >&2; exit 2; }

mkdir -p "$RESULTS_DIR"

# ── compute selected toolkits (space-separated string for portability) ────────
# --only REPLACES the selection; --enable/--all ADD to the samtools default.
if [ -n "$ONLY" ]; then
  SELECTED=" "
  IFS=',' read -r -a ONLY_LIST <<< "$ONLY"
  for t in "${ONLY_LIST[@]}"; do
    [ -n "$(tool_count "$t")" ] || { echo "unknown toolkit: $t (try --tools)" >&2; exit 2; }
    SELECTED="$SELECTED$t "
  done
else
  SELECTED=" samtools "
  if [ "$USE_ALL" = "1" ]; then
    for t in "${ALL_TOOLS_ORDERED[@]}"; do
      # Skip special toolkits (e.g. parabricks needs GPUs) unless explicitly
      # enabled below via --enable. They stay in the catalog so --tools and
      # --only still see them.
      if is-special "$t"; then continue; fi
      case "$SELECTED" in *" $t "*) ;; *) SELECTED="$SELECTED$t " ;; esac
    done
  fi
  if [ -n "$ENABLE" ]; then
    IFS=',' read -r -a EXTRA <<< "$ENABLE"
    for t in "${EXTRA[@]}"; do
      [ -n "$(tool_count "$t")" ] || { echo "unknown toolkit: $t (try --tools)" >&2; exit 2; }
      case "$SELECTED" in *" $t "*) ;; *) SELECTED="$SELECTED$t " ;; esac
    done
  fi
fi
is_selected() { case "$SELECTED" in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

# ── enumerate submodules ──────────────────────────────────────────────────────
MODULE_LIST="$RESULTS_DIR/modules.txt"
: > "$MODULE_LIST"
for t in "${ALL_TOOLS_ORDERED[@]}"; do
  is_selected "$t" || continue
  while IFS= read -r m; do
    [ -n "$m" ] && echo "$m" >> "$MODULE_LIST"
  done < <(find "$MODULES_DIR/modules/nf-core/$t" -type f -path '*/tests/main.nf.test' 2>/dev/null \
            | sed -E "s|$MODULES_DIR/modules/(.*)/tests/main\.nf\.test|\1|" \
            | sort -u)
done
TOTAL=$(wc -l < "$MODULE_LIST" | tr -d ' ')
echo "selected toolkits:$SELECTED" >&2
echo "modules to test:   $TOTAL (in $MODULE_LIST)" >&2

if [ "$DRY_RUN" = "1" ]; then
  cat "$MODULE_LIST"
  exit 0
fi

# ── per-module submitter ──────────────────────────────────────────────────────
DC_FLAGS=""
for d in "${DCS[@]}"; do DC_FLAGS+=" --datacenter $d"; done
if [ -n "$TASK_DRIVER" ]; then
  DC_FLAGS+=" --driver $TASK_DRIVER"
fi
if [ -n "$HOST_VOLUME" ]; then
  DC_FLAGS+=" --host-volume $HOST_VOLUME"
  # Default workdir convention: /<volname>/nextflow-work for non-scratch volumes
  case "$HOST_VOLUME" in
    scratch) ;;
    nextflow-work) DC_FLAGS+=" --work-dir /work/nextflow-work" ;;
    *)             DC_FLAGS+=" --work-dir /$HOST_VOLUME/nextflow-work" ;;
  esac
fi

SUBMIT_LOG="$RESULTS_DIR/submits.log"
SUBMIT_SCRIPT="$RESULTS_DIR/submit-one.sh"
cat > "$SUBMIT_SCRIPT" <<EOF
#!/usr/bin/env bash
set -uo pipefail
mod="\$1"
slug=\$(echo "\$mod" | tr '/' '-' | tr '[:upper:]' '[:lower:]')
job=\$(echo "m-\${slug}" | head -c 60)
out=\$("$ABC_BIN" module run "\$mod" --test \\
  $DC_FLAGS \\
  --pipeline-gen-url-base $RUSTFS_BASE/releases/nf-pipeline-gen \\
  --pipeline-gen-version $PG_VERSION \\
  --github-token "\$GITHUB_TOKEN" \\
  --s3-endpoint $RUSTFS_BASE \\
  --s3-access-key $S3_ACCESS_KEY \\
  --s3-secret-key $S3_SECRET_KEY \\
  --name "\$job" \\
  --nomad-addr "\$NOMAD_ADDR" \\
  --nomad-token "\$NOMAD_TOKEN" 2>&1)
rc=\$?
eval_id=\$(echo "\$out" | awk '/Eval ID/{print \$3; exit}')
if [ \$rc -ne 0 ]; then
  echo "\$mod|SUBMIT_FAIL|\$job|\$(echo "\$out" | tail -1)"
else
  echo "\$mod|SUBMITTED|\$job|\$eval_id"
fi
EOF
chmod +x "$SUBMIT_SCRIPT"

# ── stop / GC any prior m-* runs ──────────────────────────────────────────────
echo "stopping any existing m-* jobs..." >&2
curl -s -H "X-Nomad-Token: $NOMAD_TOKEN" "$NOMAD_ADDR/v1/jobs?prefix=m-" \
  | python3 -c 'import json,sys; [print(j["ID"]) for j in json.load(sys.stdin)]' \
  | xargs -P 16 -I {} curl -s -X DELETE -H "X-Nomad-Token: $NOMAD_TOKEN" "$NOMAD_ADDR/v1/job/{}" -o /dev/null
sleep 5
curl -s -X PUT -H "X-Nomad-Token: $NOMAD_TOKEN" "$NOMAD_ADDR/v1/system/gc" -o /dev/null

# ── parallel submit ───────────────────────────────────────────────────────────
echo "submitting at $(date +%H:%M:%S) (parallel=$PARALLEL)..." >&2
xargs -P "$PARALLEL" -I {} "$SUBMIT_SCRIPT" {} < "$MODULE_LIST" > "$SUBMIT_LOG" 2>&1
echo "done at      $(date +%H:%M:%S)" >&2
awk -F'|' '{print $2}' "$SUBMIT_LOG" | sort | uniq -c >&2

# ── wait for cluster to drain ─────────────────────────────────────────────────
echo "waiting ${WAIT_MIN} min for cluster..." >&2
sleep "$((WAIT_MIN * 60))"

# ── collect ───────────────────────────────────────────────────────────────────
RESULTS="$RESULTS_DIR/results.tsv"
{
  echo "module|status|gen|nf|nfr|alloc|node"
  while IFS='|' read -r mod stat job eval_id; do
    [ "$stat" = "SUBMITTED" ] || continue
    s=$(curl -s -H "X-Nomad-Token: $NOMAD_TOKEN" "$NOMAD_ADDR/v1/job/$job/allocations?all=true" | python3 -c "
import json,sys
a=sorted(json.load(sys.stdin), key=lambda x: x.get('CreateTime',0), reverse=True)
if not a: print('noalloc|-|-|-|-|-'); sys.exit(0)
x=a[0]
ts=x.get('TaskStates') or {}
nf=ts.get('nextflow') or {}
g=ts.get('generate') or {}
node=(x.get('NodeName') or '?').split('.')[0][:20]
print(x['ClientStatus']+'|'+g.get('State','?')+'|'+nf.get('State','?')+'|'+str(nf.get('Restarts',0))+'|'+x['ID'][:8]+'|'+node)
")
    echo "$mod|$s"
  done < "$SUBMIT_LOG"
} > "$RESULTS"

# ── per-tool summary ──────────────────────────────────────────────────────────
echo
echo "================ ACCEPTANCE TEST SUMMARY ================"
echo "results: $RESULTS"
echo
printf "%-12s %6s %6s %6s %6s %6s\n" "TOOLKIT" "TOTAL" "PASS" "FAIL" "OTHER" "PASS%"
overall_status=0
for t in "${ALL_TOOLS_ORDERED[@]}"; do
  is_selected "$t" || continue
  total=$(awk -F'|' -v t="$t" 'NR>1 && $1 ~ "/"t"/" {c++} END{print c+0}' "$RESULTS")
  pass=$(awk -F'|' -v t="$t" 'NR>1 && $1 ~ "/"t"/" && $2=="complete" {c++} END{print c+0}' "$RESULTS")
  fail=$(awk -F'|' -v t="$t" 'NR>1 && $1 ~ "/"t"/" && $2=="failed"   {c++} END{print c+0}' "$RESULTS")
  other=$((total - pass - fail))
  pct=0
  [ "$total" -gt 0 ] && pct=$((pass * 100 / total))
  printf "%-12s %6d %6d %6d %6d %5d%%\n" "$t" "$total" "$pass" "$fail" "$other" "$pct"
  # Fail acceptance if any selected toolkit is below 50% pass rate
  if [ "$total" -gt 0 ] && [ "$pct" -lt 50 ]; then overall_status=1; fi
done
echo

# ── failed-module detail ──────────────────────────────────────────────────────
fail_count=$(awk -F'|' 'NR>1 && $2=="failed"' "$RESULTS" | wc -l | tr -d ' ')
if [ "$fail_count" -gt 0 ]; then
  echo "Failed modules (first 30):"
  awk -F'|' 'NR>1 && $2=="failed"{print "  - "$1}' "$RESULTS" | head -30
  echo
fi

if [ "$overall_status" -eq 0 ]; then
  echo "ACCEPTANCE: PASS"
else
  echo "ACCEPTANCE: FAIL (one or more toolkits below 50% pass rate)"
fi
exit "$overall_status"
