#!/bin/bash
# ── SLURM — Hello World ───────────────────────────────────────────────────────
# Submit:  sbatch job.slurm
# Status:  squeue -j <JOBID>   |   scontrol show job <JOBID>
# Output:  hello-world-<JOBID>.out

#SBATCH --job-name=hello-world     # Job name
#SBATCH --ntasks=1                 # 1 task
#SBATCH --cpus-per-task=1          # 1 CPU per task
#SBATCH --mem=256M                 # Memory per node
#SBATCH --time=00:05:00            # Max runtime (HH:MM:SS)
#SBATCH --output=hello-world-%j.out  # %j = job ID
#SBATCH --error=hello-world-%j.err

echo "=== SLURM Hello World ==="
echo "  Host          : $(hostname)"
echo "  Job ID        : $SLURM_JOB_ID"
echo "  Job name      : $SLURM_JOB_NAME"
echo "  Partition     : $SLURM_JOB_PARTITION"
echo "  CPUs alloc    : $SLURM_CPUS_ON_NODE"
echo "  Mem alloc     : $SLURM_MEM_PER_NODE MB"
echo "  Submit dir    : $SLURM_SUBMIT_DIR"
echo "  Start time    : $(date)"
echo

echo "Hello, world from SLURM!"

echo
echo "  End time      : $(date)"
