#!/bin/bash
#SBATCH --job-name=hpc-bridge-smoke
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:02:00
#ABC --name=hpc-bridge-smoke
#ABC --driver=slurm
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=256M
#ABC --time=00:02:00
echo "HPC bridge smoke test from abc job run"
echo "NOMAD_ALLOC_ID=${NOMAD_ALLOC_ID:-unset}"
echo "SLURM_JOB_ID=${SLURM_JOB_ID:-unset}"
echo "HOSTNAME=$(hostname)"
