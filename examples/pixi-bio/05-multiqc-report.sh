#!/bin/bash
#ABC --name=multiqc-report
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml
#ABC --cores=2
#ABC --mem=8G
#ABC --time=01:00:00
#ABC --alloc_id
#
# Aggregate QC reports from fastp, FastQC, samtools flagstat/idxstats,
# and bcftools stats into a single MultiQC HTML report.
#
# Required meta keys:
#   search_dir   root directory to search for QC files (recursively)
#   outdir       where to write the MultiQC report
#
# Optional:
#   title        report title (default: "Cohort QC Report")
#   filename     report filename stem (default: multiqc_report)

set -euo pipefail

SEARCH_DIR="${NOMAD_META_SEARCH_DIR:?}"
OUTDIR="${NOMAD_META_OUTDIR:?}"
TITLE="${NOMAD_META_TITLE:-Cohort QC Report}"
FILENAME="${NOMAD_META_FILENAME:-multiqc_report}"

mkdir -p "${OUTDIR}"

echo "[multiqc] scanning ${SEARCH_DIR}"

multiqc \
  "${SEARCH_DIR}" \
  --outdir    "${OUTDIR}" \
  --filename  "${FILENAME}" \
  --title     "${TITLE}" \
  --force \
  --export \
  --cl-config 'max_table_rows: 10000'

echo "[multiqc] report → ${OUTDIR}/${FILENAME}.html"
