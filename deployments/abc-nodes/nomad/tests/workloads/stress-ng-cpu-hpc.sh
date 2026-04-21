#!/bin/sh
# CPU stress for namespace "hpc" (override with --namespace= if hpc NS is absent).
# quay.io/container-perf-tools/stress-ng (containerd-driver).
#
#ABC --name=wl-stress-cpu-hpc
#ABC --namespace=hpc
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=4
#ABC --mem=768M
#ABC --time=00:08:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=cpu_hpc_ns
set -eu
nc="${NOMAD_CPU_CORES:-4}"
case "$nc" in *[!0-9]*) nc=4;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=4; fi
echo "wl-stress-cpu-hpc: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} cpu_workers=${nc}"
exec stress-ng --cpu "$nc" --timeout 60s --metrics-brief
