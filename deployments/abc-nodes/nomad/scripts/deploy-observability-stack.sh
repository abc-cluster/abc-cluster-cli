#!/usr/bin/env bash
# Deploy abc-nodes observability jobs (MinIO for Loki S3, Prometheus, Loki, Grafana, Alloy).
# Uses the active abc context for Nomad (same as: abc admin services nomad cli -- …).
#
# Usage (from repo root analysis/packages/abc-cluster-cli):
#   ./deployments/abc-nodes/nomad/scripts/deploy-observability-stack.sh
#
# Prerequisites:
#   - Nomad clients: bridge networking + containerd-driver for MinIO/Loki/Prometheus/Grafana.
#   - MinIO bucket "loki" created (e.g. mc mb) before Loki starts, if not already present.
#   - Alloy: changing job type service→system requires a one-time purge of the old job;
#     this script does that when ABC_NODES_ALLOY_PURGE_FOR_SYSTEM=1 (default when upgrading).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# scripts → nomad → abc-nodes → deployments → abc-cluster-cli repo root
ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"
# shellcheck disable=SC2206
NOMAD_CLI=(${NOMAD:-abc admin services nomad cli --})
JOBS_DIR="${ROOT}/deployments/abc-nodes/nomad"

if ! command -v abc >/dev/null 2>&1; then
  echo "error: abc CLI not in PATH" >&2
  exit 1
fi

run() {
  echo "+ $*"
  "$@"
}

validate() {
  run "${NOMAD_CLI[@]}" job validate "$1"
}

deploy() {
  run "${NOMAD_CLI[@]}" job run -detach "$1"
}

# Core order: MinIO (Loki backend) → Prometheus → Loki → Grafana → Alloy.
validate "${JOBS_DIR}/minio.nomad.hcl"
deploy "${JOBS_DIR}/minio.nomad.hcl"

validate "${JOBS_DIR}/prometheus.nomad.hcl"
deploy "${JOBS_DIR}/prometheus.nomad.hcl"

validate "${JOBS_DIR}/loki.nomad.hcl"
deploy "${JOBS_DIR}/loki.nomad.hcl"

validate "${JOBS_DIR}/grafana.nomad.hcl"
deploy "${JOBS_DIR}/grafana.nomad.hcl"

# Alloy was historically type=service; Nomad forbids in-place type change to system.
if [[ "${ABC_NODES_ALLOY_PURGE_FOR_SYSTEM:-1}" == "1" ]] && "${NOMAD_CLI[@]}" job status abc-nodes-alloy >/dev/null 2>&1; then
  jtype=""
  if command -v jq >/dev/null 2>&1; then
    jtype="$("${NOMAD_CLI[@]}" job inspect abc-nodes-alloy | jq -r '.Type // empty' 2>/dev/null || true)"
  fi
  if [[ "$jtype" == "" ]]; then
    jtype="$("${NOMAD_CLI[@]}" job status abc-nodes-alloy 2>/dev/null | awk '/^Type/ {print $NF}' || true)"
  fi
  if [[ "$jtype" == "service" ]]; then
    echo "+ migrating abc-nodes-alloy from service to system (purge)"
    run "${NOMAD_CLI[@]}" job stop -purge abc-nodes-alloy || true
    sleep 2
  fi
fi

validate "${JOBS_DIR}/alloy.nomad.hcl"
deploy "${JOBS_DIR}/alloy.nomad.hcl"

echo "Observability stack jobs submitted. Optional: traefik after backends — ${JOBS_DIR}/traefik.nomad.hcl"
echo "E2E: ABC_INTEGRATION_OBS_STACK=1 go test -tags integration -timeout=15m -run TestIntegration_ObsStack ./cmd/job/..."
