#!/bin/bash
# ── ABC — Job Dependency: Step 1 (alignment) ─────────────────────────────────
# Submit chain — two patterns:
#
# Pattern A: ABC --depend directive (inline, inside the script)
#   abc job run job-step1.abc.sh --submit
#   (step 2 script uses  #ABC --depend=complete:wgs-step1-align  to block)
#
# Pattern B: Shell-level polling (most portable, equivalent to PBS afterok)
#   STEP1_ID=$(abc job run job-step1.abc.sh --submit | grep "Nomad job ID" | awk '{print $NF}')
#   while ! abc job status "$STEP1_ID" 2>/dev/null | grep -q "^0$"; do sleep 30; done
#   abc job run job-step2.abc.sh --submit

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=wgs-step1-align
#ABC --nodes=24
#ABC --cores=16
#ABC --mem=64G
#ABC --time=08:00:00
#ABC --driver=hpc-bridge
#ABC --dc=za-cpt-hpc1

# ── Runtime exposure ──────────────────────────────────────────────────────────
#ABC --alloc_index
#ABC --alloc_id
#ABC --cpu_cores
#ABC --task_dir
#ABC --alloc_dir             # Shared BAM staging directory across the group

# ── Migration notes (dependency) ─────────────────────────────────────────────
# PBS depend=afterok / SLURM --dependency=afterok:
#   → ABC #ABC --depend=complete:<job-id>  (prestart lifecycle hook)
#   → OR poll with: abc job status <step1-id>  exit 0 = complete
#
# Dependency granularity:
#   PBS:   afterok:$STEP1[]  — waits for entire array
#   SLURM: afterok:$STEP1    — waits for entire array
#          aftercorr:$STEP1  — waits per corresponding task index
#   ABC:   --depend=complete:wgs-step1-align waits for the job (all allocs)
#          Per-alloc dependency is not natively supported yet.

set -euo pipefail

LINE_NUM=$((NOMAD_ALLOC_INDEX + 1))
SAMPLE=$(sed -n "${LINE_NUM}p" "${NOMAD_TASK_DIR}/samplesheet.csv")
BAM_DIR="${NOMAD_ALLOC_DIR}/bam"    # ALLOC_DIR is shared: all 24 allocs write here

mkdir -p "$BAM_DIR"

echo "[step1] Sample ${NOMAD_ALLOC_INDEX} (line ${LINE_NUM}): ${SAMPLE}"
echo "  Alloc: ${NOMAD_ALLOC_ID}  Cores: ${NOMAD_CPU_CORES}"

bwa mem -t "$NOMAD_CPU_CORES" \
  "${NOMAD_TASK_DIR}/reference/GRCh38.fa" \
  "${NOMAD_TASK_DIR}/fastq/${SAMPLE}_R1.fastq.gz" \
  "${NOMAD_TASK_DIR}/fastq/${SAMPLE}_R2.fastq.gz" \
  | samtools sort -o "${BAM_DIR}/${SAMPLE}.sorted.bam"

samtools index "${BAM_DIR}/${SAMPLE}.sorted.bam"
echo "[step1] Done: ${SAMPLE}  bam:${BAM_DIR}/${SAMPLE}.sorted.bam  alloc:${NOMAD_ALLOC_ID}"
