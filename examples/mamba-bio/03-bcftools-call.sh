#!/bin/bash
#ABC --name=mamba-bcftools-call
#ABC --runtime=micromamba-exec
#ABC --from-file=environment.yml
#ABC --cores=8
#ABC --mem=16G
#ABC --time=06:00:00
#ABC --task-tmp
#ABC --mamba-cleanup
#ABC --alloc_id
#
# Single-sample variant calling with bcftools mpileup | bcftools call.
#
# Required meta keys:
#   sample     sample ID
#   bam        path to sorted, indexed BAM
#   ref        path to reference FASTA (must have .fai index)
#   outdir     destination for VCF outputs

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
BAM="${NOMAD_META_BAM:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-8}

VCF="${OUTDIR}/${SAMPLE}.vcf.gz"
mkdir -p "${OUTDIR}"

echo "[bcftools] sample=${SAMPLE} threads=${THREADS}"

bcftools mpileup \
  --threads "${THREADS}" \
  --fasta-ref "${REF}" \
  --output-type u \
  --annotate FORMAT/DP,FORMAT/AD \
  "${BAM}" \
| bcftools call \
  --threads "${THREADS}" \
  --multiallelic-caller \
  --variants-only \
  --output-type z \
  --output "${VCF}"

bcftools index --tbi --threads "${THREADS}" "${VCF}"
bcftools stats --threads "${THREADS}" "${VCF}" > "${OUTDIR}/${SAMPLE}.bcftools_stats.txt"

NVARS=$(bcftools view -H "${VCF}" | wc -l)
echo "[bcftools] called ${NVARS} variants → ${VCF}"
