#!/bin/bash
# ── ABC — Array Job ───────────────────────────────────────────────────────────
# Submits N independent allocations, one per sample in samplesheet.csv.
# Each allocation is scheduled independently across the cluster.
#
# Dry-run:  abc job run job.abc.sh --dry-run
# Submit:   abc job run job.abc.sh --submit
# Watch:    abc job run job.abc.sh --submit --watch
# Status:   abc job status array-align
# Logs:     abc job logs array-align --alloc <prefix> --follow

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=array-align
#ABC --nodes=48              # 48 parallel allocations
#                            # PBS:   #PBS -t 1-48
#                            # SLURM: #SBATCH --array=1-48
#                            #
#                            # ⚠ GAP: no max-concurrent limit
#                            #   PBS:   #PBS -t 1-48%8
#                            #   SLURM: --array=1-48%8
#                            #   ABC:   not supported — Nomad uses resource
#                            #          availability to naturally throttle
#ABC --cores=8
#ABC --mem=32G
#ABC --time=04:00:00
#ABC --driver=raw_exec

# ── Runtime exposure ──────────────────────────────────────────────────────────
#ABC --alloc_index           # 0-based index → NOMAD_ALLOC_INDEX
#                            # PBS:   $PBS_ARRAYID   (1-based)
#                            # SLURM: $SLURM_ARRAY_TASK_ID  (1-based)
#                            # ABC:   $NOMAD_ALLOC_INDEX     (0-based) ← note
#ABC --alloc_id              # Unique alloc ID → NOMAD_ALLOC_ID
#ABC --cpu_cores             # Reserved cores → NOMAD_CPU_CORES
#ABC --task_dir              # Per-alloc scratch dir → NOMAD_TASK_DIR
#ABC --alloc_dir             # Shared group dir → NOMAD_ALLOC_DIR

# ── Migration notes ───────────────────────────────────────────────────────────
# Index offset: PBS/SLURM arrays are 1-based; NOMAD_ALLOC_INDEX is 0-based.
# Use: $((NOMAD_ALLOC_INDEX + 1)) to get the 1-based row from a samplesheet.
#
# Output files: PBS/SLURM write to per-task files automatically.
# ABC: all output goes to the Nomad log stream. Retrieve with:
#   abc job logs array-align --alloc <prefix> --type stdout
# To persist output, write to $NOMAD_TASK_DIR (node-local) or mount ABC data.

set -euo pipefail

# NOMAD_ALLOC_INDEX is 0-based; add 1 for sed line number
LINE_NUM=$((NOMAD_ALLOC_INDEX + 1))
SAMPLESHEET="${NOMAD_TASK_DIR}/samplesheet.csv"
REFERENCE="${NOMAD_TASK_DIR}/reference/genome.fa"

SAMPLE=$(sed -n "${LINE_NUM}p" "$SAMPLESHEET")

echo "[ABC] Alloc $NOMAD_ALLOC_INDEX (line $LINE_NUM) / sample: $SAMPLE"
echo "  Alloc ID  : ${NOMAD_ALLOC_ID}"
echo "  CPU cores : ${NOMAD_CPU_CORES}"
echo "  Host      : $(hostname)"

bwa mem -t "$NOMAD_CPU_CORES" \
  "$REFERENCE" \
  "${NOMAD_TASK_DIR}/fastq/${SAMPLE}_R1.fastq.gz" \
  "${NOMAD_TASK_DIR}/fastq/${SAMPLE}_R2.fastq.gz" \
  > "${NOMAD_TASK_DIR}/output/${SAMPLE}.sam"

# Emit structured completion record for abc job logs parsing
echo "[ABC] Done: $SAMPLE  output:${NOMAD_TASK_DIR}/output/${SAMPLE}.sam  alloc:${NOMAD_ALLOC_ID}"
