#!/bin/bash
#SBATCH --job-name=gcp-slurm-e2e-array
#SBATCH --array=1-3
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:05:00
#SBATCH --partition=compute
#SBATCH --output=/shared/results/%x-%A_%a.out
#SBATCH --error=/shared/results/%x-%A_%a.err

set -euo pipefail

echo "SLURM_E2E_ARRAY_OK slurm_array_id=${SLURM_ARRAY_TASK_ID:-unset} nomad_alloc_index=${NOMAD_ALLOC_INDEX:-unset} alloc=${NOMAD_ALLOC_ID:-unknown}"
