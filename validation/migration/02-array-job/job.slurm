#!/bin/bash
# ── SLURM — Array Job ─────────────────────────────────────────────────────────
# Submits N independent tasks, one per sample in samplesheet.csv.
#
# Submit:   sbatch job.slurm
# Status:   squeue --array -j <JOBID>
# Cancel:   scancel <JOBID>_<INDEX>
# Output:   array-align-<JOBID>_<INDEX>.out

#SBATCH --job-name=array-align
#SBATCH --cpus-per-task=8         # 8 cores per task
#SBATCH --mem=32G                 # Memory per task
#SBATCH --time=04:00:00
#SBATCH --array=1-48%8            # 48 tasks, max 8 concurrent  ← ABC gap
#SBATCH --output=array-align-%A_%a.out   # %A = job ID, %a = array index
#SBATCH --error=array-align-%A_%a.err

SAMPLESHEET="${SLURM_SUBMIT_DIR}/samplesheet.csv"
REFERENCE="${SLURM_SUBMIT_DIR}/reference/genome.fa"

# SLURM_ARRAY_TASK_ID is 1-based
SAMPLE=$(sed -n "${SLURM_ARRAY_TASK_ID}p" "$SAMPLESHEET")

echo "[SLURM] Task $SLURM_ARRAY_TASK_ID / sample: $SAMPLE"
echo "  CPUs:   $SLURM_CPUS_PER_TASK"
echo "  Mem:    $SLURM_MEM_PER_NODE MB"
echo "  Host:   $(hostname)"
echo "  NodeID: $SLURM_NODEID"

bwa mem -t "$SLURM_CPUS_PER_TASK" \
  "$REFERENCE" \
  "${SLURM_SUBMIT_DIR}/fastq/${SAMPLE}_R1.fastq.gz" \
  "${SLURM_SUBMIT_DIR}/fastq/${SAMPLE}_R2.fastq.gz" \
  > "${SLURM_SUBMIT_DIR}/output/${SAMPLE}.sam"

echo "[SLURM] Done: $SAMPLE"
