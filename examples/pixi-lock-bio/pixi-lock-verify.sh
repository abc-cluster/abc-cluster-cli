#!/bin/bash
#ABC --name=pixi-lock-verify
#ABC --runtime=pixi-exec
#ABC --from=pixi.lock
#ABC --time=00:15:00

set -euo pipefail

echo "[pixi-lock-verify] pixi version: $(pixi --version)"
echo "[pixi-lock-verify] pigz: $(pigz --version 2>&1 | head -1)"
echo "[pixi-lock-verify] samtools: $(samtools --version | head -1)"
echo "[pixi-lock-verify] PIXI_HOME=${PIXI_HOME}"
echo "[pixi-lock-verify] SUCCESS (locked install)"
