#!/bin/bash
#ABC --name=bcftools-call
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml
#ABC --cores=8
#ABC --mem=16G
#ABC --time=06:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Call variants with bcftools mpileup + call, then filter and annotate.
# Produces a hard-filtered VCF and a stats report.
#
# Required meta keys:
#   sample     sample ID
#   bam        path to sorted, indexed BAM
#   ref        path to reference FASTA (must have .fai index)
#   outdir     destination for VCF + stats
#
# Optional:
#   regions    comma-separated regions, e.g. "chr1,chr2" (default: whole genome)
#   min_dp     minimum read depth to emit a site (default: 5)
#   min_qual   minimum variant quality PHRED (default: 20)

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
BAM="${NOMAD_META_BAM:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_CORES:-8}}

REGIONS="${NOMAD_META_REGIONS:-}"
MIN_DP="${NOMAD_META_MIN_DP:-5}"
MIN_QUAL="${NOMAD_META_MIN_QUAL:-20}"

RAW_VCF="${OUTDIR}/${SAMPLE}_raw.vcf.gz"
FILT_VCF="${OUTDIR}/${SAMPLE}_filtered.vcf.gz"

mkdir -p "${OUTDIR}"

echo "[call] sample=${SAMPLE} bam=$(basename "${BAM}") threads=${THREADS}"

REGION_ARGS=()
if [[ -n "${REGIONS}" ]]; then
  REGION_ARGS=(--regions "${REGIONS}")
fi

# Pileup → call
bcftools mpileup \
  --fasta-ref    "${REF}" \
  --output-type  u \
  --annotate     FORMAT/DP,FORMAT/AD,FORMAT/ADF,FORMAT/ADR,FORMAT/SP,INFO/AD \
  --threads      "${THREADS}" \
  "${REGION_ARGS[@]}" \
  "${BAM}" \
| bcftools call \
  --multiallelic-caller \
  --variants-only \
  --output-type z \
  --threads "${THREADS}" \
  --output  "${RAW_VCF}"

bcftools index --tbi --threads "${THREADS}" "${RAW_VCF}"

# Hard filter: depth + quality + strand bias
bcftools filter \
  --include "FORMAT/DP >= ${MIN_DP} && QUAL >= ${MIN_QUAL} && INFO/RPB > 0.1" \
  --soft-filter LowQual \
  --output-type z \
  --threads "${THREADS}" \
  --output  "${FILT_VCF}" \
  "${RAW_VCF}"

bcftools index --tbi --threads "${THREADS}" "${FILT_VCF}"

# Stats
bcftools stats \
  --fasta-ref "${REF}" \
  --threads   "${THREADS}" \
  "${FILT_VCF}" \
  > "${OUTDIR}/${SAMPLE}_bcftools_stats.txt"

bcftools stats \
  --fasta-ref "${REF}" \
  --split-by-ID \
  --threads   "${THREADS}" \
  "${FILT_VCF}" \
  > "${OUTDIR}/${SAMPLE}_bcftools_stats_by_id.txt"

# Quick summary to stdout
PASS_SNP=$(bcftools view  --type snps  --apply-filters PASS "${FILT_VCF}" | bcftools stats | awk '/^SN.*number of SNPs:/ {print $NF}')
PASS_IND=$(bcftools view  --type indels --apply-filters PASS "${FILT_VCF}" | bcftools stats | awk '/^SN.*number of indels:/ {print $NF}')
echo "[call] PASS SNPs=${PASS_SNP}  indels=${PASS_IND} → ${FILT_VCF}"
