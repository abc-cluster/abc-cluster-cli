#!/bin/bash
#ABC --name=nomad-smoke
#ABC --driver=raw_exec
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=128M
set -euo pipefail
echo "NOMAD smoke test"
echo "HOSTNAME=$(hostname)"
echo "DATE=$(date -Is)"
