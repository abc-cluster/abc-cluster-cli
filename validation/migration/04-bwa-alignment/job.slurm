#!/bin/bash
# ── SLURM — BWA-MEM Alignment Pipeline (realistic) ───────────────────────────
# Full short-read alignment for a cohort of WGS samples.
# Assumes samplesheet.csv has one sample ID per line.
#
# Submit:  sbatch job.slurm
# Monitor: squeue --array -j <JOBID>
#          sacct -j <JOBID> --format=JobID,State,Elapsed,MaxRSS
# Cancel:  scancel <JOBID>

#SBATCH --job-name=bwa-align
#SBATCH --cpus-per-task=16
#SBATCH --mem=64G
#SBATCH --time=08:00:00
#SBATCH --array=1-96%16            # 96 tasks, max 16 concurrent   ← ABC gap
#SBATCH --output=bwa-align-%A_%a.out
#SBATCH --error=bwa-align-%A_%a.err
#SBATCH --mail-type=FAIL,END       # Email on fail or end          ← ABC gap
#SBATCH --mail-user=user@institution.ac.za                       # ← ABC gap
#SBATCH --requeue                  # Requeue on node failure       ← ABC gap

set -euo pipefail

# ── Paths ─────────────────────────────────────────────────────────────────────
WORKDIR="$SLURM_SUBMIT_DIR"
SAMPLESHEET="$WORKDIR/samplesheet.csv"
REF="$WORKDIR/reference/GRCh38/GRCh38.fa"
FASTQ_DIR="$WORKDIR/fastq"
OUT_DIR="$WORKDIR/output"

SAMPLE=$(sed -n "${SLURM_ARRAY_TASK_ID}p" "$SAMPLESHEET")
mkdir -p "$OUT_DIR"

# ── Run ───────────────────────────────────────────────────────────────────────
echo "[SLURM] Sample $SLURM_ARRAY_TASK_ID: $SAMPLE"
echo "  Job:    $SLURM_JOB_ID  Array task: $SLURM_ARRAY_TASK_ID"
echo "  Node:   $(hostname)"
echo "  CPUs:   $SLURM_CPUS_PER_TASK"
echo "  Mem:    ${SLURM_MEM_PER_NODE} MB"

# Alignment
bwa mem \
  -t "$SLURM_CPUS_PER_TASK" \
  -R "@RG\tID:${SAMPLE}\tSM:${SAMPLE}\tPL:ILLUMINA\tLB:${SAMPLE}_lib1" \
  "$REF" \
  "${FASTQ_DIR}/${SAMPLE}_R1.fastq.gz" \
  "${FASTQ_DIR}/${SAMPLE}_R2.fastq.gz" \
  | samtools sort -@ 4 -m 4G \
  -o "${OUT_DIR}/${SAMPLE}.sorted.bam"

samtools index "${OUT_DIR}/${SAMPLE}.sorted.bam"
samtools flagstat "${OUT_DIR}/${SAMPLE}.sorted.bam" \
  > "${OUT_DIR}/${SAMPLE}.flagstat.txt"

# SLURM accounting: sacct will capture elapsed time and peak RSS
echo "[SLURM] Done: ${SAMPLE} -> ${OUT_DIR}/${SAMPLE}.sorted.bam"
