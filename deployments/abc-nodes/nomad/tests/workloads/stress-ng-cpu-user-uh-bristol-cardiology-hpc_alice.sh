#!/bin/sh
# Stress-ng for uh-bristol-cardiology-hpc_alice — container-perf-tools/stress-ng.
#
#ABC --name=uh-bristol-cardiology-hpc_alice--wl-str
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=quay.io/container-perf-tools/stress-ng:latest
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=research_user=uh-bristol-cardiology-hpc_alice
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=user_alice_default_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=stress-ng group=cardiology"
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
exec stress-ng --cpu "$nc" --timeout 45s --metrics-brief
