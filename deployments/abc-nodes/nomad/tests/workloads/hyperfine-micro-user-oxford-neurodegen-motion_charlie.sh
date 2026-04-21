#!/bin/bash
# Hyperfine micro-benchmarks for oxford-neurodegen-motion_charlie (namespace default).
#
#ABC --name=oxford-neurodegen-motion_charlie--wl-hf
#ABC --namespace=default
#ABC --driver=containerd
#ABC --driver.config.image=docker.io/library/debian:bookworm-slim
#ABC --cores=1
#ABC --mem=512M
#ABC --time=00:08:00
#ABC --namespace
#ABC --job_name
#ABC --meta=research_user=oxford-neurodegen-motion_charlie
#ABC --meta=workload=hyperfine
#ABC --meta=scenario=user_charlie_default_ns
set -euo pipefail
echo "research_user=${NOMAD_META_RESEARCH_USER:-} workload=hyperfine group=motion"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq hyperfine ca-certificates >/dev/null
exec hyperfine --runs 6 --warmup 1 'sleep 0.01' 'wc -c /proc/cpuinfo'
