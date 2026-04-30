#!/bin/bash
#ABC --name=align-bwa
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml
#ABC --cores=16
#ABC --mem=32G
#ABC --time=08:00:00
#ABC --task-tmp
#ABC --alloc_id
#
# Align trimmed paired-end reads with bwa-mem2, sort and index with samtools.
# Marks duplicates inline using samtools markdup.
#
# Required meta keys:
#   sample     sample ID
#   r1         trimmed R1 fastq.gz
#   r2         trimmed R2 fastq.gz
#   ref        path to reference FASTA (must already be bwa-mem2 indexed)
#   outdir     destination for BAM + index
#
# Optional:
#   rg_lb      read-group library  (default: lib1)
#   rg_pl      read-group platform (default: ILLUMINA)

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
R1="${NOMAD_META_R1:?}"
R2="${NOMAD_META_R2:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_CORES:-16}}

RG_LB="${NOMAD_META_RG_LB:-lib1}"
RG_PL="${NOMAD_META_RG_PL:-ILLUMINA}"

BAM="${OUTDIR}/${SAMPLE}.bam"
TMPBAM="${TMPDIR:-/tmp}/${SAMPLE}_unsorted.bam"

mkdir -p "${OUTDIR}"

echo "[align] sample=${SAMPLE} ref=$(basename "${REF}") threads=${THREADS}"

RG="@RG\tID:${SAMPLE}\tSM:${SAMPLE}\tLB:${RG_LB}\tPL:${RG_PL}"

bwa-mem2 mem \
  -t "${THREADS}" \
  -R "${RG}" \
  "${REF}" "${R1}" "${R2}" \
| samtools sort \
  -@ "${THREADS}" \
  -m 2G \
  -T "${TMPDIR:-/tmp}/${SAMPLE}_sort" \
  -o "${TMPBAM}"

samtools markdup \
  -@ "${THREADS}" \
  --reference "${REF}" \
  "${TMPBAM}" "${BAM}"

samtools index -@ "${THREADS}" "${BAM}"

samtools flagstat -@ "${THREADS}" "${BAM}" > "${OUTDIR}/${SAMPLE}.flagstat"
samtools idxstats "${BAM}" > "${OUTDIR}/${SAMPLE}.idxstats"

rm -f "${TMPBAM}"

echo "[align] done → ${BAM}"
