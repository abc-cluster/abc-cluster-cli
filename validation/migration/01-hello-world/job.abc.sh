#!/bin/bash
# ── ABC — Hello World ─────────────────────────────────────────────────────────
# Submit (dry-run):  abc job run job.abc.sh --dry-run
# Submit:            abc job run job.abc.sh --submit
# Status:            abc job status hello-world
# Logs:              abc job logs hello-world --follow

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=hello-world      # Job name
#ABC --nodes=1               # 1 allocation (equivalent to 1 PBS node / SLURM task)
#ABC --cores=1               # 1 CPU core
#ABC --mem=256M              # Memory (K/M/G suffix — no 'b')
#ABC --time=00:05:00         # Walltime limit (HH:MM:SS)
#ABC --driver=exec           # Run directly on host (no container)
#ABC --output=job.out        # Logical stdout file path in metadata
#ABC --error=job.err         # Logical stderr file path in metadata

# ── Runtime exposure (replaces PBS_JOBID / SLURM_JOB_ID) ─────────────────────
#ABC --alloc_id              # Full allocation UUID  → NOMAD_ALLOC_ID
#ABC --alloc_name            # <job>.<group>[<index>] → NOMAD_ALLOC_NAME
#ABC --job_id                # Nomad job ID          → NOMAD_JOB_ID

# ── Notes ─────────────────────────────────────────────────────────────────────
# PBS  → ABC mapping
#   $PBS_JOBID        → $NOMAD_ALLOC_ID     (unique execution ID)
#   $PBS_JOBNAME      → $NOMAD_JOB_NAME     (via --job_name directive)
#   $PBS_O_WORKDIR    → $NOMAD_TASK_DIR     (via --task_dir directive)
#   #PBS -o / -e      → abc job logs [--type stdout|stderr]
#   #PBS -q <queue>   → #ABC --dc=<datacenter>
#
# SLURM → ABC mapping
#   $SLURM_JOB_ID     → $NOMAD_ALLOC_ID
#   $SLURM_JOB_NAME   → $NOMAD_JOB_ID
#   $SLURM_SUBMIT_DIR → $NOMAD_TASK_DIR
#   $SLURM_JOB_PARTITION → #ABC --dc=<datacenter>
#   --output / --error → abc job logs [--type stdout|stderr]

set -euo pipefail

echo "=== ABC Hello World ==="
echo "  Host        : $(hostname)"
echo "  Alloc ID    : ${NOMAD_ALLOC_ID}"
echo "  Alloc name  : ${NOMAD_ALLOC_NAME}"
echo "  Job ID      : ${NOMAD_JOB_ID}"
echo "  Task dir    : ${NOMAD_TASK_DIR}"
echo "  Start time  : $(date)"
echo

echo "Hello, world from ABC!"

echo
echo "  End time    : $(date)"
