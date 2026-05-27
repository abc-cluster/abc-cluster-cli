#!/bin/bash
#ABC --name=align-bwa-locked
#ABC --runtime=pixi-exec
#ABC --from-file=pixi.lock
#ABC --cores=16
#ABC --mem=32G
#ABC --time=08:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Align paired-end reads with BWA-MEM2, sort and index.
# Uses pixi.lock for exact package versions.
#
# Required meta keys:
#   sample     sample ID
#   r1         R1 fastq.gz (trimmed)
#   r2         R2 fastq.gz (trimmed)
#   ref        reference FASTA with BWA-MEM2 index
#   outdir     destination for BAM + index

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
R1="${NOMAD_META_R1:?}"
R2="${NOMAD_META_R2:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-16}
MIN_MAPQ="${NOMAD_META_MIN_MAPQ:-20}"

BAM="${OUTDIR}/${SAMPLE}.bam"
mkdir -p "${OUTDIR}"

echo "[bwa-mem2/locked] sample=${SAMPLE} threads=${THREADS}"

bwa-mem2 mem \
  -t "${THREADS}" \
  -R "@RG\tID:${SAMPLE}\tSM:${SAMPLE}\tPL:ILLUMINA\tLB:${SAMPLE}" \
  "${REF}" "${R1}" "${R2}" \
| samtools view \
  --min-MQ "${MIN_MAPQ}" \
  -@ "${THREADS}" \
  -b \
| samtools sort \
  -@ "${THREADS}" \
  -m 3G \
  -T "${TMPDIR:-/tmp}/${SAMPLE}_sort" \
  -o "${BAM}"

samtools index -@ "${THREADS}" "${BAM}"
samtools flagstat -@ "${THREADS}" "${BAM}" > "${OUTDIR}/${SAMPLE}.flagstat"

MAPPED=$(awk '/mapped \(/{print $1}' "${OUTDIR}/${SAMPLE}.flagstat" | head -1)
echo "[bwa-mem2/locked] mapped reads=${MAPPED} → ${BAM}"
