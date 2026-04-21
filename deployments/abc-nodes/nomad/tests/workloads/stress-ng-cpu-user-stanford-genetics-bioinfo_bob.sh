#!/bin/sh
# Stress-ng for stanford-genetics-bioinfo_bob in namespace services — container-perf-tools/stress-ng.
#
#ABC --name=stanford-genetics-bioinfo_bob--wl-str
#ABC --namespace=services
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=research_user=stanford-genetics-bioinfo_bob
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=user_bob_services_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=stress-ng group=bioinfo"
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
exec stress-ng --cpu "$nc" --timeout 45s --metrics-brief
