#!/bin/bash
#ABC --name=pixi-verify
#ABC --runtime=pixi-exec
#ABC --from-file=pixi-min.toml
#ABC --time=00:15:00

set -euo pipefail

echo "[pixi-verify] pixi version: $(pixi --version)"
echo "[pixi-verify] pigz: $(pigz --version 2>&1 | head -1)"
echo "[pixi-verify] PIXI_HOME=${PIXI_HOME}"
echo "[pixi-verify] SUCCESS"
