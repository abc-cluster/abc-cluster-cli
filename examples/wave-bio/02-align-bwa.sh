#!/bin/bash
#ABC --name=wave-align-bwa
#ABC --runtime=wave-exec
#ABC --from-file=environment.yml
#ABC --driver=docker
#ABC --cores=16
#ABC --mem=32G
#ABC --time=08:00:00
#ABC --task-tmp
#ABC --alloc_id
#
# Align paired-end reads with BWA-MEM2, sort and index with samtools.
#
# Uses wave-exec: the Wave-built container already contains bwa-mem2 and
# samtools — no conda setup time at runtime.
#
# Required meta keys:
#   sample     sample ID
#   r1         path to trimmed R1 fastq.gz
#   r2         path to trimmed R2 fastq.gz
#   ref        path to reference FASTA (must have BWA-MEM2 index files)
#   outdir     destination for BAM + index
#
# Optional:
#   min_mapq   minimum mapping quality filter (default: 20)
#
# Example:
#   abc job run 02-align-bwa.sh \
#     --meta sample=SRR123 \
#     --meta r1=/data/trimmed/SRR123_R1.trimmed.fastq.gz \
#     --meta r2=/data/trimmed/SRR123_R2.trimmed.fastq.gz \
#     --meta ref=/data/ref/GRCh38.fa \
#     --meta outdir=/data/bam

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

echo "[bwa-mem2] sample=${SAMPLE} threads=${THREADS}"

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
echo "[bwa-mem2] mapped reads=${MAPPED} → ${BAM}"
