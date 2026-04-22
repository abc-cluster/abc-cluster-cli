#!/bin/sh
# CPU stress in namespace "services" — hyperfine_stress-ng Wave image (containerd-driver).
#
#ABC --name=wl-stress-cpu-services
#ABC --namespace=services
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
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
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
jitter=$(( (seed + $$) % 4 ))
dur=$(( 40 + (jitter * 10) ))
echo "wl-stress-cpu-services: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} cpu_workers=${nc} duration_s=${dur} seed=${seed}"
exec stress-ng --cpu "$nc" --timeout "${dur}s" --metrics-brief
