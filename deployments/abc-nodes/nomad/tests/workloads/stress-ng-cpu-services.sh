#!/bin/sh
# CPU stress in namespace "services" — quay.io/container-perf-tools/stress-ng (containerd-driver).
#
#ABC --name=wl-stress-cpu-services
#ABC --namespace=services
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=cpu_short_services_ns
set -eu
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
echo "wl-stress-cpu-services: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} cpu_workers=${nc}"
exec stress-ng --cpu "$nc" --timeout 45s --metrics-brief
