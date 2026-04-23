#!/usr/bin/env bash
# redeploy-grafana-dashboards.sh
#
# 1) Refresh Grafana dashboard template variables from ACL / MinIO sources.
# 2) Redeploy the Grafana Nomad job so provisioning picks up JSON changes.
#
# Usage (from repo root = analysis/packages/abc-cluster-cli):
#   export ABC_ACTIVE_CONTEXT=abc-cluster-admin   # or any context with job run rights
#   bash deployments/abc-nodes/nomad/scripts/redeploy-grafana-dashboards.sh
#
# Env:
#   NOMAD_CLI  Optional prefix instead of default "abc admin services nomad cli --"
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NOMAD_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck disable=SC2206
NOMAD_CLI=(${NOMAD_CLI:-abc admin services nomad cli --})

if ! command -v abc >/dev/null 2>&1; then
  echo "error: abc CLI not in PATH" >&2
  exit 1
fi

echo "+ bash ${NOMAD_DIR}/sync-grafana-definitions.sh"
bash "${NOMAD_DIR}/sync-grafana-definitions.sh"

echo "+ ${NOMAD_CLI[*]} job run ${NOMAD_DIR}/grafana.nomad.hcl"
"${NOMAD_CLI[@]}" job run "${NOMAD_DIR}/grafana.nomad.hcl"

echo "Done."
