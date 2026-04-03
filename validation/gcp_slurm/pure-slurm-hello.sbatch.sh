#!/bin/bash
#SBATCH --job-name=gcp-slurm-e2e-hello
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:05:00
#SBATCH --partition=compute
#SBATCH --output=/shared/results/%x-%j.out
#SBATCH --error=/shared/results/%x-%j.err

set -euo pipefail

echo "SLURM_E2E_HELLO_OK job=${SLURM_JOB_ID:-unknown} host=$(hostname) alloc=${NOMAD_ALLOC_ID:-unknown}"
echo "SLURM_E2E_HELLO_ERR job=${SLURM_JOB_ID:-unknown}" >&2
