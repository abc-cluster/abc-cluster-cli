#!/bin/sh
# hyperfine micro-benchmarks in namespace "services".
# Image ships hyperfine + stress-ng (no apt at runtime).
#
#ABC --name=wl-hyperfine-micro-services
#ABC --namespace=services
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=1
#ABC --mem=512M
#ABC --time=00:08:00
#ABC --namespace
#ABC --job_name
#ABC --meta=workload=hyperfine
#ABC --meta=scenario=micro_services_ns
set -eu
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
runs=$(( 7 + ((seed + $$) % 5) ))
warmup=$(( 1 + ((seed / 2 + $$) % 2) ))
sleep_ms=$(( 10 + ((seed / 7 + $$) % 35) ))
sleep_cmd="sleep 0.$(printf '%03d' "$sleep_ms")"
echo "wl-hyperfine-micro-services: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} NOMAD_JOB_NAME=${NOMAD_JOB_NAME:-} runs=${runs} warmup=${warmup} sleep_ms=${sleep_ms}"
exec hyperfine --runs "$runs" --warmup "$warmup" \
  "$sleep_cmd" \
  'wc -c /proc/cpuinfo' \
  'uname -s'
