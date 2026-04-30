#!/bin/bash
#ABC --name=trim-reads
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Trim paired-end reads with fastp, then QC with FastQC.
#
# Required meta keys (pass via --meta or #ABC --meta):
#   sample     sample ID used for output filenames
#   r1         path to R1 fastq.gz
#   r2         path to R2 fastq.gz
#   outdir     destination directory for trimmed reads + reports
#
# Example:
#   abc job run 01-trim-reads.sh \
#     --runtime pixi --from pixi.toml \
#     --meta sample=SRR123 \
#     --meta r1=/data/raw/SRR123_R1.fastq.gz \
#     --meta r2=/data/raw/SRR123_R2.fastq.gz \
#     --meta outdir=/data/trimmed

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?meta key 'sample' is required}"
R1="${NOMAD_META_R1:?meta key 'r1' is required}"
R2="${NOMAD_META_R2:?meta key 'r2' is required}"
OUTDIR="${NOMAD_META_OUTDIR:?meta key 'outdir' is required}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_CORES:-8}}

mkdir -p "${OUTDIR}"

echo "[trim] sample=${SAMPLE} threads=${THREADS} alloc=${NOMAD_ALLOC_ID:-local}"

fastp \
  --in1  "${R1}" \
  --in2  "${R2}" \
  --out1 "${OUTDIR}/${SAMPLE}_R1_trimmed.fastq.gz" \
  --out2 "${OUTDIR}/${SAMPLE}_R2_trimmed.fastq.gz" \
  --json "${OUTDIR}/${SAMPLE}_fastp.json" \
  --html "${OUTDIR}/${SAMPLE}_fastp.html" \
  --thread "${THREADS}" \
  --detect_adapter_for_pe \
  --qualified_quality_phred 20 \
  --length_required 36 \
  --correction \
  --overrepresentation_analysis

fastqc \
  --threads "${THREADS}" \
  --outdir  "${OUTDIR}" \
  "${OUTDIR}/${SAMPLE}_R1_trimmed.fastq.gz" \
  "${OUTDIR}/${SAMPLE}_R2_trimmed.fastq.gz"

echo "[trim] done → ${OUTDIR}"
