#!/bin/bash
#ABC --name=wave-bcftools-call
#ABC --runtime=wave-exec
#ABC --from=environment.yml
#ABC --driver=docker
#ABC --cores=8
#ABC --mem=16G
#ABC --time=06:00:00
#ABC --task-tmp
#ABC --alloc_id
#
# Single-sample variant calling with bcftools mpileup | bcftools call.
#
# Uses wave-exec: bcftools and htslib are pre-installed in the Wave container.
#
# Required meta keys:
#   sample     sample ID
#   bam        path to sorted, indexed BAM
#   ref        path to reference FASTA (must have .fai index)
#   outdir     destination for VCF outputs
#
# Optional:
#   region     genomic region to restrict calling, e.g. "chr1:1-10000000"
#              (omit to call whole genome)
#
# Example:
#   abc job run 03-bcftools-call.sh \
#     --meta sample=SRR123 \
#     --meta bam=/data/bam/SRR123.bam \
#     --meta ref=/data/ref/GRCh38.fa \
#     --meta outdir=/data/vcf

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
BAM="${NOMAD_META_BAM:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-8}
REGION="${NOMAD_META_REGION:-}"

VCF="${OUTDIR}/${SAMPLE}.vcf.gz"
mkdir -p "${OUTDIR}"

echo "[bcftools] sample=${SAMPLE} threads=${THREADS}"

REGION_ARGS=()
if [[ -n "${REGION}" ]]; then
  echo "[bcftools] restricting to region=${REGION}"
  REGION_ARGS=(--regions "${REGION}")
fi

bcftools mpileup \
  --threads "${THREADS}" \
  --fasta-ref "${REF}" \
  --output-type u \
  --annotate FORMAT/DP,FORMAT/AD \
  "${REGION_ARGS[@]}" \
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
