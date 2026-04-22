#!/bin/sh
# Hyperfine micro-benchmarks for oxford-neurodegen-neuropsychiatry_charlie (namespace default).
# Image ships hyperfine + stress-ng (no apt at runtime).
#
#ABC --name=oxford-neurodegen-neuropsychiatry_charlie--wl-hf
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=1
#ABC --mem=512M
#ABC --time=00:08:00
#ABC --namespace
#ABC --job_name
#ABC --meta=research_user=oxford-neurodegen-neuropsychiatry_charlie
#ABC --meta=workload=hyperfine
#ABC --meta=scenario=user_charlie_default_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=hyperfine group=neuropsychiatry"
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
runs=$(( 6 + ((seed + $$) % 4) ))
warmup=1
sleep_ms=$(( 7 + ((seed / 11 + $$) % 24) ))
sleep_cmd="sleep 0.$(printf '%03d' "$sleep_ms")"
exec hyperfine --runs "$runs" --warmup "$warmup" "$sleep_cmd" 'wc -c /proc/cpuinfo'
