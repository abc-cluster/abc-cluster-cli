#!/bin/bash
#SBATCH --job-name=slurm-smoke
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:02:00
echo "SLURM smoke test from abc job run"
echo "SLURM_JOB_ID=${SLURM_JOB_ID:-unset}"
echo "HOSTNAME=$(hostname)"
