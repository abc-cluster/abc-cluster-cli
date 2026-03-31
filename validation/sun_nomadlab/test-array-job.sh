#!/bin/bash
# test-array-job.sh
#
# Validation job that exercises:
#   - Array dispatch via --nodes (count > 1)
#   - Runtime exposure directives (--alloc_index, --alloc_id, --cpu_cores)
#   - Meta passthrough (--meta)
#   - Short walltime (--time)
#
# Submit with:
#   abc job run validation/sun_nomadlab/test-array-job.sh --submit
#
# Or dry-run:
#   abc job run validation/sun_nomadlab/test-array-job.sh --dry-run

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=abc-validation-array
#ABC --nodes=3
#ABC --cores=1
#ABC --mem=256M
#ABC --time=00:05:00
#ABC --driver=raw_exec
#ABC --priority=30

# ── Runtime exposure directives ───────────────────────────────────────────────
#ABC --alloc_id
#ABC --alloc_index
#ABC --alloc_name
#ABC --job_id
#ABC --cpu_cores
#ABC --task_dir

# ── Meta passthrough ──────────────────────────────────────────────────────────
#ABC --meta=validation_run=true
#ABC --meta=suite=abc-job-commands

# ── Job body ──────────────────────────────────────────────────────────────────
set -euo pipefail

echo "=== ABC Validation Array Job ==="
echo "  Alloc index  : ${NOMAD_ALLOC_INDEX}"
echo "  Alloc ID     : ${NOMAD_ALLOC_ID}"
echo "  Alloc name   : ${NOMAD_ALLOC_NAME}"
echo "  Job ID       : ${NOMAD_JOB_ID}"
echo "  CPU cores    : ${NOMAD_CPU_CORES}"
echo "  Task dir     : ${NOMAD_TASK_DIR}"
echo "  Meta run     : ${NOMAD_META_VALIDATION_RUN}"
echo "  Meta suite   : ${NOMAD_META_SUITE}"
echo

# Simulate index-based sharding (like a real bioinformatics array job).
SAMPLES=("sample-A" "sample-B" "sample-C")
IDX="${NOMAD_ALLOC_INDEX:-0}"
SAMPLE="${SAMPLES[$IDX]:-unknown}"

echo "  Processing   : $SAMPLE"
sleep 2
echo "  Done         : $SAMPLE (alloc ${NOMAD_ALLOC_ID:0:8})"
echo
echo "output:${NOMAD_TASK_DIR}/${SAMPLE}.result alloc:${NOMAD_ALLOC_ID}"
