#!/bin/sh
# Stress-ng for su-sdsct-ceri_tj in namespace su-sdsct-ceri.
#
#ABC --name=su-sdsct-ceri_tj--wl-str
#ABC --namespace=su-sdsct-ceri
#ABC --driver=containerd
#ABC --driver.config.image=community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
#ABC --cores=2
#ABC --mem=768M
#ABC --time=00:07:00
#ABC --cpu_cores
#ABC --namespace
#ABC --meta=research_user=su-sdsct-ceri_tj
#ABC --meta=workload=stress-ng
#ABC --meta=scenario=user_tj_su_sdsct_ceri_ns
set -eu
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=stress-ng group=su-sdsct-ceri"
nc="${NOMAD_CPU_CORES:-2}"
case "$nc" in *[!0-9]*) nc=2;; esac
if [ "$nc" -lt 1 ] 2>/dev/null; then nc=2; fi
seed="${ABC_WORKLOAD_SEED:-$(date +%s)}"
jitter=$(( (seed + $$) % 4 ))
dur=$(( 45 + (jitter * 10) ))
exec stress-ng --cpu "$nc" --timeout "${dur}s" --metrics-brief
