#!/bin/sh
# CPU stress in namespace "default" using prebuilt OCI (shell + stress-ng in image).
# Image: quay.io/container-perf-tools/stress-ng:latest — Nomad task driver containerd-driver.
#
# Dry-run: abc job run deployments/abc-nodes/nomad/tests/workloads/stress-ng-cpu-default.sh
# Submit:  abc job run deployments/abc-nodes/nomad/tests/workloads/stress-ng-cpu-default.sh --submit
#
#ABC --name=wl-stress-cpu-default
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=cpu_short
set -eu
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
echo "wl-stress-cpu-default: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} cpu_workers=${nc}"
exec stress-ng --cpu "$nc" --timeout 45s --metrics-brief
