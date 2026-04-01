#!/bin/bash
# ── ABC — BWA-MEM Alignment Pipeline (realistic) ─────────────────────────────
# Full short-read alignment for a cohort of WGS samples.
# Assumes samplesheet.csv has one sample ID per line, uploaded to ABC data
# storage and pre-staged into NOMAD_TASK_DIR by the driver.
#
# Dry-run:  abc job run job.abc.sh --dry-run --region za-cpt
# Submit:   abc job run job.abc.sh --submit  --region za-cpt
# Watch all:abc job logs bwa-align --follow
# Per alloc:abc job logs bwa-align --alloc <8char-prefix> --follow
# Status:   abc job status bwa-align   # exits 0=done, 1=failed, 2=running

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=bwa-align
#ABC --region=za-cpt
#ABC --dc=za-cpt-hpc1
#ABC --nodes=96              # 96 parallel allocations (one per sample)
#ABC --cores=16
#ABC --mem=64G
#ABC --time=08:00:00
#ABC --driver=hpc-bridge     # Preserves site module system

# ── Runtime exposure ──────────────────────────────────────────────────────────
#ABC --alloc_index           # 0-based sample index → NOMAD_ALLOC_INDEX
#ABC --alloc_id              # Unique ID → NOMAD_ALLOC_ID  (use in output paths)
#ABC --alloc_name            # human label → NOMAD_ALLOC_NAME
#ABC --cpu_cores             # → NOMAD_CPU_CORES  (pass to -t flag)
#ABC --mem_limit             # → NOMAD_MEMORY_LIMIT MB  (for JVM/R heap sizing)
#ABC --task_dir              # per-alloc scratch → NOMAD_TASK_DIR
#ABC --alloc_dir             # shared group dir  → NOMAD_ALLOC_DIR
#ABC --dc                    # datacenter landed → NOMAD_DC

# ── Meta passthrough ──────────────────────────────────────────────────────────
#ABC --meta=pipeline=bwa-mem2
#ABC --meta=reference=GRCh38
#ABC --meta=cohort=ZA-WGS-2024-Q4

# ── Migration notes ───────────────────────────────────────────────────────────
# SLURM --array=1-96%16 (max concurrent): not supported in ABC.
#   → Nomad schedules all 96 naturally limited by available CPU/mem resources.
#
# SLURM --mail-type / PBS -m ae (email notifications): not in ABC.
#   → Use abc job status in a cron/automation loop, or abc job logs --follow.
#
# SLURM --requeue (restart on node failure): not in ABC.
#   → Nomad reschedules failed allocs automatically if rescheduler policy is set.
#
# SLURM sacct (accounting): not in ABC.
#   → abc job show bwa-align shows alloc counts; cost API planned.
#
# $PBS_O_WORKDIR / $SLURM_SUBMIT_DIR:
#   → NOMAD_TASK_DIR (per-alloc) or NOMAD_ALLOC_DIR (shared across allocations)
#   → Stage data in via `abc data upload` before submitting the job.

set -euo pipefail

# ── Paths ─────────────────────────────────────────────────────────────────────
# In ABC, input data is staged into NOMAD_TASK_DIR by the driver.
SAMPLESHEET="${NOMAD_TASK_DIR}/samplesheet.csv"
REF="${NOMAD_TASK_DIR}/reference/GRCh38/GRCh38.fa"
FASTQ_DIR="${NOMAD_TASK_DIR}/fastq"
OUT_DIR="${NOMAD_TASK_DIR}/output"

# NOMAD_ALLOC_INDEX is 0-based; PBS_ARRAYID / SLURM_ARRAY_TASK_ID are 1-based
LINE_NUM=$((NOMAD_ALLOC_INDEX + 1))
SAMPLE=$(sed -n "${LINE_NUM}p" "$SAMPLESHEET")

mkdir -p "$OUT_DIR"

# ── Diagnostics ───────────────────────────────────────────────────────────────
echo "[ABC] Sample index ${NOMAD_ALLOC_INDEX} (line ${LINE_NUM}): ${SAMPLE}"
echo "  Alloc     : ${NOMAD_ALLOC_ID}"
echo "  Alloc name: ${NOMAD_ALLOC_NAME}"
echo "  DC        : ${NOMAD_DC}"
echo "  CPU cores : ${NOMAD_CPU_CORES}"
echo "  Mem limit : ${NOMAD_MEMORY_LIMIT} MB"
echo "  Task dir  : ${NOMAD_TASK_DIR}"
echo "  Meta      : cohort=${NOMAD_META_COHORT}  ref=${NOMAD_META_REFERENCE}"

# ── Alignment ─────────────────────────────────────────────────────────────────
bwa mem \
  -t "$NOMAD_CPU_CORES" \
  -R "@RG\tID:${SAMPLE}\tSM:${SAMPLE}\tPL:ILLUMINA\tLB:${SAMPLE}_lib1" \
  "$REF" \
  "${FASTQ_DIR}/${SAMPLE}_R1.fastq.gz" \
  "${FASTQ_DIR}/${SAMPLE}_R2.fastq.gz" \
  | samtools sort -@ 4 -m 4G \
  -o "${OUT_DIR}/${SAMPLE}.sorted.bam"

samtools index "${OUT_DIR}/${SAMPLE}.sorted.bam"
samtools flagstat "${OUT_DIR}/${SAMPLE}.sorted.bam" \
  > "${OUT_DIR}/${SAMPLE}.flagstat.txt"

# ── Structured completion record ──────────────────────────────────────────────
# This line is parseable by downstream automation and `abc job logs` filters.
echo "[ABC] Done sample:${SAMPLE} bam:${OUT_DIR}/${SAMPLE}.sorted.bam alloc:${NOMAD_ALLOC_ID} dc:${NOMAD_DC}"
