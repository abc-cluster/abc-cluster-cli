#!/bin/bash
# ── ABC — Job Dependency: Step 2 (variant calling) ───────────────────────────
# Runs after wgs-step1-align completes (all 24 allocations succeeded).
#
# Submit (after step 1 is complete):
#   abc job run job-step2.abc.sh --submit
#
# Or use inline --depend to block until step 1 is done:
#   abc job run job-step2.abc.sh --submit
#   (the --depend directive below causes Nomad to schedule this only after
#    wgs-step1-align reaches "complete" status)
#
# Shell-level polling alternative (no --depend needed):
#   until abc job status wgs-step1-align; [ $? -eq 0 ]; do
#     echo "Waiting for step 1..."; sleep 60
#   done
#   abc job run job-step2.abc.sh --submit

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=wgs-step2-variantcall
#ABC --nodes=1                              # Single GATK joint calling task
#ABC --cores=16
#ABC --mem=128G
#ABC --time=12:00:00
#ABC --driver=hpc-bridge
#ABC --dc=za-cpt-hpc1
#ABC --depend=complete:wgs-step1-align      # Block on step 1 (prestart hook)
#                                           # PBS:   -W depend=afterok:<JOBID>
#                                           # SLURM: --dependency=afterok:<JOBID>

# ── Runtime exposure ──────────────────────────────────────────────────────────
#ABC --alloc_id
#ABC --cpu_cores
#ABC --task_dir
#ABC --alloc_dir             # Reads BAMs written by step 1 from shared dir

# ── Meta ─────────────────────────────────────────────────────────────────────
#ABC --meta=pipeline=gatk-haplotypecaller
#ABC --meta=reference=GRCh38
#ABC --meta=step=2

set -euo pipefail

echo "[step2] GATK HaplotypeCaller — joint genotyping"
echo "  Alloc : ${NOMAD_ALLOC_ID}"
echo "  Cores : ${NOMAD_CPU_CORES}"
echo "  Mem   : ${NOMAD_MEMORY_LIMIT} MB"

BAM_DIR="${NOMAD_ALLOC_DIR}/bam"
OUT_DIR="${NOMAD_TASK_DIR}/output"
mkdir -p "$OUT_DIR"

# Collect all BAMs produced by step 1 via the shared alloc dir
BAM_ARGS=()
while IFS= read -r bam; do
  BAM_ARGS+=("-I" "$bam")
done < <(find "$BAM_DIR" -name "*.sorted.bam" | sort)

echo "  BAMs  : ${#BAM_ARGS[@]} / 2"

JAVA_HEAP_MB=$(( NOMAD_MEMORY_LIMIT - 2048 ))

gatk HaplotypeCaller \
  --java-options "-Xmx${JAVA_HEAP_MB}m" \
  -R "${NOMAD_TASK_DIR}/reference/GRCh38.fa" \
  "${BAM_ARGS[@]}" \
  -O "${OUT_DIR}/cohort.g.vcf.gz" \
  -ERC GVCF

echo "[step2] Done  vcf:${OUT_DIR}/cohort.g.vcf.gz  alloc:${NOMAD_ALLOC_ID}"
