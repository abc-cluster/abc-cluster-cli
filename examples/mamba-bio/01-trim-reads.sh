#!/bin/bash
#ABC --name=mamba-trim-reads
#ABC --runtime=micromamba-exec
#ABC --from-file=environment.yml
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --mamba-cleanup
#ABC --alloc_id
#
# Trim paired-end reads with fastp, then QC with FastQC.
# Uses micromamba-exec to create and activate a conda environment
# from the bundled environment.yml at job start.
#
# Required meta keys:
#   sample     sample ID used for output filenames
#   r1         path to R1 fastq.gz
#   r2         path to R2 fastq.gz
#   outdir     destination directory for trimmed reads + reports
#
# Optional:
#   min_length minimum read length after trimming (default: 50)
#   threads    explicit thread count (default: NOMAD_CPU_CORES)
#
# Example:
#   abc job run 01-trim-reads.sh \
#     --runtime micromamba --from environment.yml \
#     --meta sample=SRR123 \
#     --meta r1=/data/raw/SRR123_R1.fastq.gz \
#     --meta r2=/data/raw/SRR123_R2.fastq.gz \
#     --meta outdir=/data/trimmed

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
R1="${NOMAD_META_R1:?}"
R2="${NOMAD_META_R2:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_THREADS:-8}}
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

echo "[fastqc] running on trimmed reads"
fastqc \
  --threads "${THREADS}" \
  --outdir  "${OUTDIR}" \
  "${OUTDIR}/${SAMPLE}_R1.trimmed.fastq.gz" \
  "${OUTDIR}/${SAMPLE}_R2.trimmed.fastq.gz"

echo "[trim] done → ${OUTDIR}"
