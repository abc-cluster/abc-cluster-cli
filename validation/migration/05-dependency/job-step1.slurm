#!/bin/bash
# ── SLURM — Job Dependency: Step 1 (alignment) ───────────────────────────────
# Submit chain:
#   STEP1=$(sbatch --parsable job-step1.slurm)
#   echo "Step 1 job: $STEP1"
#   sbatch --dependency=afterok:$STEP1 job-step2.slurm
#
# SLURM dependency types:
#   afterok       — after all tasks in job succeed
#   afterany      — after job ends regardless of status
#   afternotok    — after job fails
#   aftercorr     — after corresponding array task succeeds
#   singleton     — wait for all jobs with same name to finish

#SBATCH --job-name=wgs-step1-align
#SBATCH --cpus-per-task=16
#SBATCH --mem=64G
#SBATCH --time=08:00:00
#SBATCH --array=1-24
#SBATCH --output=step1-%A_%a.out
#SBATCH --error=step1-%A_%a.err

SAMPLE=$(sed -n "${SLURM_ARRAY_TASK_ID}p" "$SLURM_SUBMIT_DIR/samplesheet.csv")
echo "[step1] Aligning: $SAMPLE"

bwa mem -t "$SLURM_CPUS_PER_TASK" \
  "$SLURM_SUBMIT_DIR/reference/GRCh38.fa" \
  "$SLURM_SUBMIT_DIR/fastq/${SAMPLE}_R1.fastq.gz" \
  "$SLURM_SUBMIT_DIR/fastq/${SAMPLE}_R2.fastq.gz" \
  | samtools sort -o "$SLURM_SUBMIT_DIR/bam/${SAMPLE}.sorted.bam"

samtools index "$SLURM_SUBMIT_DIR/bam/${SAMPLE}.sorted.bam"
echo "[step1] Done: $SAMPLE"
