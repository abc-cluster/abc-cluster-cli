#!/bin/bash
#ABC --name=trim-reads-locked
#ABC --runtime=pixi-exec
#ABC --from-file=pixi.lock
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Trim paired-end reads with fastp + FastQC.
# Uses pixi.lock for bit-for-bit reproducibility — every run installs
# the exact same package versions regardless of upstream channel updates.
#
# Generate pixi.lock first:  cd examples/pixi-lock-bio && pixi install
#
# Required meta keys:
#   sample     sample ID
#   r1         path to R1 fastq.gz
#   r2         path to R2 fastq.gz
#   outdir     destination directory

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
R1="${NOMAD_META_R1:?}"
R2="${NOMAD_META_R2:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-8}
MIN_LEN="${NOMAD_META_MIN_LENGTH:-50}"

mkdir -p "${OUTDIR}"

echo "[fastp] sample=${SAMPLE} threads=${THREADS}"

fastp \
  --in1  "${R1}" \
  --in2  "${R2}" \
  --out1 "${OUTDIR}/${SAMPLE}_R1.trimmed.fastq.gz" \
  --out2 "${OUTDIR}/${SAMPLE}_R2.trimmed.fastq.gz" \
  --json "${OUTDIR}/${SAMPLE}.fastp.json" \
  --html "${OUTDIR}/${SAMPLE}.fastp.html" \
  --thread "${THREADS}" \
  --length_required "${MIN_LEN}" \
  --detect_adapter_for_pe \
  --correction \
  --overrepresentation_analysis \
  2>&1 | tee "${OUTDIR}/${SAMPLE}.fastp.log"

fastqc \
  --threads "${THREADS}" \
  --outdir  "${OUTDIR}" \
  "${OUTDIR}/${SAMPLE}_R1.trimmed.fastq.gz" \
  "${OUTDIR}/${SAMPLE}_R2.trimmed.fastq.gz"

echo "[trim-locked] done → ${OUTDIR}"
