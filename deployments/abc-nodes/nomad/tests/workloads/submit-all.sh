#!/usr/bin/env bash
# Submit sample workload scripts in this directory (stress-ng + hyperfine, multiple namespaces).
# User-scoped scripts (institute-dept-group_user in #ABC --name=…--wl-…) are included for Grafana per-user rows.
# Prereqs: abc in PATH, Nomad reachable per your active abc context or env, containerd-driver (OCI) enabled on clients.
#
# Usage (repo root = analysis/packages/abc-cluster-cli):
#   ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh
# Detach without log follow:
#   ABC_JOB_FLAGS="--submit" ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh
# Follow logs (default):
#   ABC_JOB_FLAGS="--submit --watch" ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh

set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
FLAGS=(--submit --watch)
if [[ -n "${ABC_JOB_FLAGS:-}" ]]; then
  # shellcheck disable=SC2206
  FLAGS=(${ABC_JOB_FLAGS})
fi

shopt -s nullglob
for script in "$HERE"/*.sh; do
  base=$(basename "$script")
  [[ "$base" == submit-all.sh ]] && continue
  echo "=== abc job run ${FLAGS[*]} $script ==="
  abc job run "$script" "${FLAGS[@]}"
done
