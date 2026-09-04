#!/bin/sh
# Stress-ng for uh-bristol-animaltb-hpc_alice — hyperfine_stress-ng Wave image.
#
#ABC --name=uh-bristol-animaltb-hpc_alice--wl-str
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=2
#ABC --mem=512M
#ABC --time=00:06:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=research_user=uh-bristol-animaltb-hpc_alice
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=user_alice_default_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=stress-ng group=animaltb"
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
jitter=$(( (seed + $$) % 3 ))
dur=$(( 42 + (jitter * 9) ))
exec stress-ng --cpu "$nc" --timeout "${dur}s" --metrics-brief
