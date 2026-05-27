#!/bin/bash
#ABC --name=minimap2-long
#ABC --runtime=pixi-exec
#ABC --from-file=pixi.toml
#ABC --cores=16
#ABC --mem=64G
#ABC --time=12:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
#
# Align long reads (Oxford Nanopore or PacBio HiFi) with minimap2.
# Supports ONT (map-ont) and PacBio HiFi (map-hifi) presets.
#
# Required meta keys:
#   sample     sample ID
#   reads      path to FASTQ/FASTQ.gz (can be single file or directory of files)
#   ref        path to reference FASTA (plain or .gz)
#   outdir     destination for BAM + index
#
# Optional:
#   preset     minimap2 preset: map-ont (default) or map-hifi
#   min_mapq   minimum mapping quality to keep (default: 20)

set -euo pipefail

SAMPLE="${NOMAD_META_SAMPLE:?}"
READS="${NOMAD_META_READS:?}"
REF="${NOMAD_META_REF:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
THREADS=${NOMAD_CPU_CORES:-${NOMAD_META_CORES:-16}}

PRESET="${NOMAD_META_PRESET:-map-ont}"
MIN_MAPQ="${NOMAD_META_MIN_MAPQ:-20}"

BAM="${OUTDIR}/${SAMPLE}.bam"

mkdir -p "${OUTDIR}"

echo "[minimap2] sample=${SAMPLE} preset=${PRESET} threads=${THREADS}"

# If READS is a directory, concatenate all fastq/fastq.gz files
if [[ -d "${READS}" ]]; then
  INPUT_FILES=$(find "${READS}" -name "*.fastq.gz" -o -name "*.fastq" -o -name "*.fq.gz" -o -name "*.fq" | sort)
  echo "[minimap2] ${INPUT_FILES}" | wc -l | xargs echo "  input files:"
  READ_ARG=( ${INPUT_FILES} )
else
  READ_ARG=( "${READS}" )
fi

minimap2 \
  -ax "${PRESET}" \
  -t  "${THREADS}" \
  -R  "@RG\tID:${SAMPLE}\tSM:${SAMPLE}\tPL:${PRESET}" \
  --secondary=no \
  "${REF}" \
  "${READ_ARG[@]}" \
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
samtools coverage "${BAM}" > "${OUTDIR}/${SAMPLE}.coverage.tsv"

MAPPED=$(awk '/mapped \(/{print $1}' "${OUTDIR}/${SAMPLE}.flagstat" | head -1)
echo "[minimap2] mapped reads=${MAPPED} → ${BAM}"
