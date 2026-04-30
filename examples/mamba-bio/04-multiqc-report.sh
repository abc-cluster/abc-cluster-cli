#!/bin/bash
#ABC --name=mamba-multiqc-report
#ABC --runtime=micromamba-exec
#ABC --from=environment.yml
#ABC --cores=4
#ABC --mem=8G
#ABC --time=01:00:00
#ABC --task-tmp
#ABC --mamba-cleanup
#ABC --alloc_id
#
# Aggregate QC metrics from fastp + FastQC + samtools into a MultiQC report.
#
# Required meta keys:
#   indir      directory containing fastp JSON, FastQC zip, samtools flagstat files
#   outdir     destination for the MultiQC report
#
# Optional:
#   title      report title (default: "MultiQC Report")
#   project    project name in report (default: value of indir)

set -euo pipefail

INDIR="${NOMAD_META_INDIR:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
TITLE="${NOMAD_META_TITLE:-MultiQC Report}"

mkdir -p "${OUTDIR}"

echo "[multiqc] scanning ${INDIR}"

multiqc \
  --title "${TITLE}" \
  --outdir "${OUTDIR}" \
  --force \
  --export \
  "${INDIR}"

echo "[multiqc] report → ${OUTDIR}/multiqc_report.html"
