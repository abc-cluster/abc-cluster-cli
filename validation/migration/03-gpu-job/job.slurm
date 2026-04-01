#!/bin/bash
# ── SLURM — GPU Job ───────────────────────────────────────────────────────────
# Example: Kraken2 taxonomic classification using GPU-accelerated model.
#
# Submit:  sbatch job.slurm
# Status:  squeue -j <JOBID>  |  scontrol show job <JOBID>

#SBATCH --job-name=gpu-kraken2
#SBATCH --ntasks=1
#SBATCH --cpus-per-task=8
#SBATCH --mem=64G
#SBATCH --time=02:00:00
#SBATCH --gres=gpu:nvidia_a100:2    # 2 NVIDIA A100 GPUs
#SBATCH --partition=gpu             # GPU partition
#SBATCH --output=gpu-kraken2-%j.out
#SBATCH --error=gpu-kraken2-%j.err

# CUDA visible devices is set automatically by SLURM via cgroups
module load cuda/11.8
module load kraken2/2.1.3

echo "[SLURM] GPU job: $SLURM_JOB_ID"
echo "  Host     : $(hostname)"
echo "  GPUs     : $SLURM_GPUS_ON_NODE"
echo "  CUDA dev : $CUDA_VISIBLE_DEVICES"
nvidia-smi --query-gpu=name,memory.total --format=csv,noheader

kraken2 \
  --threads "$SLURM_CPUS_PER_TASK" \
  --db "${SLURM_SUBMIT_DIR}/krakendb" \
  --output "${SLURM_SUBMIT_DIR}/output/classified.tsv" \
  --report "${SLURM_SUBMIT_DIR}/output/report.txt" \
  "${SLURM_SUBMIT_DIR}/input/reads.fastq.gz"

echo "[SLURM] Done"
