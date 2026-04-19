#!/usr/bin/env bash
# End-to-end check: Nomad job stdout reaches Loki and Prometheus answers (same gates as
# TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive).
#
# Usage (from repo root analysis/packages/abc-cluster-cli):
#   export ABC_CONFIG_FILE=~/.abc/config.yaml   # active abc-nodes context
#   ./deployments/abc-nodes/nomad/scripts/e2e-observability-stack.sh
#
# Requires: Go, abc config with admin Loki/Prometheus URLs and Nomad token.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"
cd "${ROOT}"

export ABC_INTEGRATION_OBS_STACK="${ABC_INTEGRATION_OBS_STACK:-1}"
export ABC_CONFIG_FILE="${ABC_CONFIG_FILE:-${HOME}/.abc/config.yaml}"
export ABC_INTEGRATION_LOKI_WAIT_SEC="${ABC_INTEGRATION_LOKI_WAIT_SEC:-180}"

exec go test -tags integration -count=1 -timeout=15m \
  -run TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive \
  ./cmd/job/...
