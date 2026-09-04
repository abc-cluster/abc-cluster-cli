#!/bin/sh
# CPU stress; scheduler namespace from active abc context (no #ABC --namespace= value).
# hyperfine_stress-ng Wave image (containerd-driver).
#
#   abc context use <tenant-context>
#   abc job run .../stress-ng-cpu-abc-context.sh
#
#ABC --name=wl-stress-cpu-abc-context
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
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
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
jitter=$(( (seed + $$) % 4 ))
dur=$(( 43 + (jitter * 11) ))
echo "wl-stress-cpu-abc-context: cpu_workers=${nc} duration_s=${dur} seed=${seed}"
exec stress-ng --cpu "$nc" --timeout "${dur}s" --metrics-brief
