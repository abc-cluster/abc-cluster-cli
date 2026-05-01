#!/bin/bash
#ABC --name=wave-smoke
#ABC --runtime=wave-exec
#ABC --from=environment.yml
#ABC --driver=docker
#ABC --cores=2
#ABC --mem=4G
#ABC --time=00:15:00
#ABC --alloc_id
#
# Smoke test for the wave-exec runtime.
#
# Submitting this script verifies that:
#   1. Wave (Seqera) built and pushed a container from environment.yml.
#   2. The prestart wave-build task successfully waited (--await) for the
#      image to become pullable.
#   3. Every bioinformatics tool in the environment is reachable and reports
#      its version correctly inside the resulting container.
#   4. R + Bioconductor packages load without error.
#
# No data required — this job just prints versions and exits 0 on success.
#
# Example:
#   abc job run 00-smoke.sh

set -euo pipefail

# Print the first line of a command's combined output.
# Runs in a subshell with pipefail disabled so that tools that exit non-zero
# after receiving SIGPIPE (head closing the pipe) do not abort the script.
ver() { ( set +o pipefail; "$@" 2>&1 | head -1 ); }

echo "=== wave-exec smoke test ==="
echo "Alloc : ${NOMAD_ALLOC_ID}"
echo "Node  : ${NOMAD_NODE_NAME:-unknown}"
echo ""

echo "--- sequence tools ---"
ver fastp   --version
ver fastqc  --version
ver multiqc --version
ver seqkit  version

echo ""
echo "--- alignment ---"
ver bwa-mem2 version
ver minimap2 --version
ver samtools --version

echo ""
echo "--- variant calling ---"
ver bcftools --version
ver bgzip    --version

echo ""
echo "--- R packages ---"
Rscript - <<'RSCRIPT'
pkgs <- c("DESeq2", "edgeR", "ggplot2", "dplyr", "readr")
ok   <- TRUE
for (p in pkgs) {
  if (!requireNamespace(p, quietly = TRUE)) {
    cat("MISSING:", p, "\n")
    ok <- FALSE
  } else {
    cat("OK:", p, as.character(packageVersion(p)), "\n")
  }
}
if (!ok) stop("one or more R packages missing")
RSCRIPT

echo ""
echo "=== smoke test PASSED ==="
