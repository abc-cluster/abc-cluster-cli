#!/bin/bash
# ── ABC — GPU Job ─────────────────────────────────────────────────────────────
# Example: Kraken2 taxonomic classification using GPU-accelerated model.
#
# Dry-run:  abc job run job.abc.sh --dry-run
# Submit:   abc job run job.abc.sh --submit
# Logs:     abc job logs gpu-kraken2 --follow

# ── Scheduler directives ──────────────────────────────────────────────────────
#ABC --name=gpu-kraken2
#ABC --nodes=1
#ABC --cores=8
#ABC --mem=64G
#ABC --gpus=2                # Reserve 2 GPUs  ← maps to nvidia/gpu device block
#                            # PBS:   nodes=1:ppn=8:gpus=2
#                            # SLURM: --gres=gpu:nvidia_a100:2
#                            #
#                            # ⚠ GAP: GPU type/model selection not supported.
#                            #   SLURM: --gres=gpu:nvidia_a100:2
#                            #   PBS:   nodes=1:ppn=8:gpus=2:gpu_type=A100
#                            #   ABC:   --gpus=2  (count only; type via node
#                            #          constraints is a future feature)
#ABC --time=02:00:00
#ABC --driver=raw_exec
#ABC --dc=gpu-dc1            # Target GPU-equipped datacenter

# ── Runtime exposure ──────────────────────────────────────────────────────────
#ABC --alloc_id
#ABC --cpu_cores             # → NOMAD_CPU_CORES  (replaces $SLURM_CPUS_PER_TASK)
#ABC --task_dir              # → NOMAD_TASK_DIR   (replaces $SLURM_SUBMIT_DIR)

# ── Migration notes ───────────────────────────────────────────────────────────
# GPU device visibility: Nomad sets CUDA_VISIBLE_DEVICES automatically via the
# device plugin, same as SLURM cgroups. No change needed in the script body.
#
# Partitions / queues: ABC uses datacenters (#ABC --dc=gpu-dc1) to target
# GPU-equipped nodes. Unlike SLURM partitions, this is a placement constraint
# rather than a separate scheduling queue.
#
# Module system: not available in ABC's raw_exec driver by default.
# Options:
#   a) Use hpc-bridge driver which integrates with site module systems
#   b) Use docker driver with a CUDA image
#   c) Pre-load binaries via ABC data upload to NOMAD_TASK_DIR

set -euo pipefail

echo "[ABC] GPU job: ${NOMAD_ALLOC_ID}"
echo "  Host      : $(hostname)"
echo "  CPU cores : ${NOMAD_CPU_CORES}"
echo "  Task dir  : ${NOMAD_TASK_DIR}"

# Nomad sets CUDA_VISIBLE_DEVICES from the device reservation
echo "  CUDA devs : ${CUDA_VISIBLE_DEVICES:-<not set>}"
if command -v nvidia-smi &>/dev/null; then
  nvidia-smi --query-gpu=name,memory.total --format=csv,noheader
fi

kraken2 \
  --threads "$NOMAD_CPU_CORES" \
  --db "${NOMAD_TASK_DIR}/krakendb" \
  --output "${NOMAD_TASK_DIR}/output/classified.tsv" \
  --report "${NOMAD_TASK_DIR}/output/report.txt" \
  "${NOMAD_TASK_DIR}/input/reads.fastq.gz"

echo "[ABC] Done  alloc:${NOMAD_ALLOC_ID}"
