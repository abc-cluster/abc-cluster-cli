#!/bin/bash
#ABC --name=bcftools-cohort
#ABC --runtime=pixi-exec
#ABC --from-file=pixi.toml
#ABC --cores=8
#ABC --mem=32G
#ABC --time=12:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Merge per-sample VCFs, apply cohort-level filters, compute population stats.
# Expects individual filtered VCFs produced by 03-bcftools-call.sh.
#
# Required meta keys:
#   vcf_list   newline- or space-separated list of per-sample VCF paths,
#              OR path to a text file containing one VCF path per line
#   ref        path to reference FASTA
#   outdir     destination for merged VCF + stats
#   cohort     cohort/project label used in output names
#
# Optional:
#   min_ac     minimum allele count to retain a site after merging (default: 1)
#   max_miss   maximum fraction of missing genotypes per site (default: 0.2)

set -euo pipefail

VCF_LIST="${NOMAD_META_VCF_LIST:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
COHORT="${NOMAD_META_COHORT:?}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_CORES:-8}}

MIN_AC="${NOMAD_META_MIN_AC:-1}"
MAX_MISS="${NOMAD_META_MAX_MISS:-0.2}"

MERGED="${OUTDIR}/${COHORT}_merged.vcf.gz"
FILT="${OUTDIR}/${COHORT}_cohort_filtered.vcf.gz"

mkdir -p "${OUTDIR}"

# Build the list of input VCFs — accept a file or a direct value.
if [[ -f "${VCF_LIST}" ]]; then
  LIST_FILE="${VCF_LIST}"
else
  LIST_FILE="${TMPDIR:-/tmp}/${COHORT}_vcf_list.txt"
  tr ' ' '\n' <<< "${VCF_LIST}" | grep -v '^$' > "${LIST_FILE}"
fi

N_SAMPLES=$(wc -l < "${LIST_FILE}")
echo "[cohort] merging ${N_SAMPLES} samples → ${MERGED}"

# Index any un-indexed inputs
while IFS= read -r vcf; do
  [[ -f "${vcf}.tbi" ]] || bcftools index --tbi --threads 2 "${vcf}"
done < "${LIST_FILE}"

bcftools merge \
  --file-list  "${LIST_FILE}" \
  --output-type z \
  --threads    "${THREADS}" \
  --output     "${MERGED}"

bcftools index --tbi --threads "${THREADS}" "${MERGED}"

# Cohort filter: allele count, missingness, biallelic only
bcftools view \
  --min-ac     "${MIN_AC}" \
  --max-alleles 2 \
  --output-type u \
  "${MERGED}" \
| bcftools filter \
  --exclude "F_MISSING > ${MAX_MISS}" \
  --soft-filter HighMiss \
  --output-type z \
  --threads "${THREADS}" \
  --output  "${FILT}"

bcftools index --tbi --threads "${THREADS}" "${FILT}"

# Population-level stats
bcftools stats \
  --fasta-ref "${REF}" \
  --threads   "${THREADS}" \
  "${FILT}" \
  > "${OUTDIR}/${COHORT}_stats.txt"

# Ts/Tv per-sample
bcftools stats \
  --fasta-ref "${REF}" \
  --samples-file <(bcftools query -l "${FILT}") \
  --threads "${THREADS}" \
  "${FILT}" \
  > "${OUTDIR}/${COHORT}_per_sample_stats.txt"

SITES=$(bcftools view -H "${FILT}" | wc -l)
echo "[cohort] ${SITES} PASS sites in ${FILT}"
