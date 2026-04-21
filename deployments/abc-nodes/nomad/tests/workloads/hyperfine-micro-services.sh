#!/bin/bash
# hyperfine micro-benchmarks in namespace "services".
#
#ABC --name=wl-hyperfine-micro-services
#ABC --namespace=services
#ABC --driver=containerd
#ABC --driver.config.image=docker.io/library/debian:bookworm-slim
#ABC --cores=1
#ABC --mem=512M
#ABC --time=00:08:00
#ABC --namespace
#ABC --job_name
#ABC --meta=workload=hyperfine
#ABC --meta=scenario=micro_services_ns
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq hyperfine ca-certificates >/dev/null
echo "wl-hyperfine-micro-services: NOMAD_NAMESPACE=${NOMAD_NAMESPACE:-} NOMAD_JOB_NAME=${NOMAD_JOB_NAME:-}"
exec hyperfine --runs 8 --warmup 2 \
  'sleep 0.01' \
  'wc -c /proc/cpuinfo' \
  'uname -s'
