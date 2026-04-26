#!/bin/sh
# Minimal hello-world smoke job for abc CLI submission testing.
# Use this first to validate context token, namespace ACL, and containerd execution.
#
# Preview HCL:
#   abc job run deployments/abc-nodes/nomad/tests/workloads/hello-world-default.sh --no-submit
# Submit (default) and stream logs:
#   abc job run deployments/abc-nodes/nomad/tests/workloads/hello-world-default.sh --watch
#
#ABC --name=wl-hello-world-default
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=1
#ABC --mem=256M
#ABC --time=00:03:00
#ABC --namespace
#ABC --job_name
#ABC --meta=workload=hello-world
#ABC --meta=scenario=cli_smoke
set -eu

echo "hello from abc-nodes"
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "namespace=${NOMAD_NAMESPACE:-unknown}"
echo "job_name=${NOMAD_JOB_NAME:-unknown}"
echo "node_name=${NOMAD_NODE_NAME:-unknown}"
echo "alloc_id=${NOMAD_ALLOC_ID:-unknown}"
echo "task_name=${NOMAD_TASK_NAME:-unknown}"
echo "done"
