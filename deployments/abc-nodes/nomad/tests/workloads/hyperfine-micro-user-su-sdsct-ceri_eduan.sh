#!/bin/sh
# Hyperfine micro-benchmarks for su-sdsct-ceri_eduan in namespace su-sdsct-ceri.
# Image ships hyperfine + stress-ng (no apt at runtime).
#
#ABC --name=su-sdsct-ceri_eduan--wl-hf
#ABC --namespace=su-sdsct-ceri
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=1
#ABC --mem=512M
#ABC --time=00:08:00
#ABC --namespace
#ABC --job_name
#ABC --meta=research_user=su-sdsct-ceri_eduan
#ABC --meta=workload=hyperfine
#ABC --meta=scenario=user_eduan_su_sdsct_ceri_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=hyperfine group=su-sdsct-ceri"
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
runs=$(( 6 + ((seed + $$) % 4) ))
warmup=1
sleep_ms=$(( 9 + ((seed / 7 + $$) % 30) ))
sleep_cmd="sleep 0.$(printf '%03d' "$sleep_ms")"
exec hyperfine --runs "$runs" --warmup "$warmup" "$sleep_cmd" 'wc -c /proc/cpuinfo'
