#!/bin/bash
#SBATCH --job-name=hybrid-slurm-smoke
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:02:00
#ABC --name=hybrid-slurm-smoke
#ABC --driver=slurm
#ABC --dc=gcp-slurm
echo "Hybrid preamble smoke test"
echo "HOSTNAME=$(hostname)"
echo "SLURM_JOB_ID=${SLURM_JOB_ID:-unset}"
