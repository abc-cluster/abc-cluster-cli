#!/bin/bash
#ABC --name=mamba-verify
#ABC --runtime=micromamba-exec
#ABC --from-file=environment.yml
#ABC --time=00:45:00

set -euo pipefail

echo "[mamba-verify] micromamba version: $(micromamba --version)"
echo "[mamba-verify] python: $(python --version)"
echo "[mamba-verify] pigz: $(pigz --version 2>&1 | head -1)"
echo "[mamba-verify] samtools: $(samtools --version | head -1)"
echo "[mamba-verify] bcftools: $(bcftools --version | head -1)"
echo "[mamba-verify] MAMBA_ROOT_PREFIX=${MAMBA_ROOT_PREFIX}"
echo "[mamba-verify] SUCCESS"
