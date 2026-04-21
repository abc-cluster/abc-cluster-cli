#!/bin/sh
# CPU stress; scheduler namespace from active abc context (no #ABC --namespace= value).
# quay.io/container-perf-tools/stress-ng (containerd-driver).
#
#   abc context use <tenant-context>
#   abc job run .../stress-ng-cpu-abc-context.sh --submit
#
#ABC --name=wl-stress-cpu-abc-context
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=cpu_abc_context_ns
set -eu
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
echo "wl-stress-cpu-abc-context: cpu_workers=${nc}"
exec stress-ng --cpu "$nc" --timeout 45s --metrics-brief
