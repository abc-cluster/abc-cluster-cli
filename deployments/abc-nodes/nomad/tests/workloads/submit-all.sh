#!/usr/bin/env bash
# Submit sample workload scripts in this directory (stress-ng + hyperfine, multiple namespaces).
# Default behavior is randomized profile overrides to better simulate real multi-user patterns.
# User-scoped scripts (institute-dept-group_user in #ABC --name=…--wl-…) are included for Grafana per-user rows.
#
# For overlapping jobs on live research namespaces + su-<ns>_<user> principals from MinIO bootstrap,
# use run-grafana-multi-user-burst.sh (see docs/abc-nodes-observability-and-operations.md).
# Prereqs: abc in PATH, Nomad reachable per your active abc context or env, containerd-driver (OCI) enabled on clients.
#
# Usage (repo root = analysis/packages/abc-cluster-cli):
#   ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh
# Deterministic randomization:
#   ABC_SEED=123 ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh
# Disable randomization:
#   ABC_RANDOMIZE=0 ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh
# Override submit flags:
#   ABC_JOB_FLAGS="--submit --watch" ./deployments/abc-nodes/nomad/tests/workloads/submit-all.sh

set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
FLAGS=(--submit)
if [[ -n "${ABC_JOB_FLAGS:-}" ]]; then
  # shellcheck disable=SC2206
  FLAGS=(${ABC_JOB_FLAGS})
fi

RANDOMIZE=${ABC_RANDOMIZE:-1}
SEED=${ABC_SEED:-$(date +%s)}

a_users=(
  "cambridge-ai-systems_dana"
  "eth-zurich-cv_lab_eric"
  "mit-systems-bio_fiona"
  "ucl-neuro-hpc_george"
)

rand_mod() {
  local mod="$1"
  SEED=$(( (SEED * 1103515245 + 12345) & 2147483647 ))
  echo $(( SEED % mod ))
}

profile_for_script() {
  local script_name="$1"
  local kind
  local idx

  if [[ "$script_name" == stress-ng-* ]]; then
    kind="stress"
  else
    kind="hyperfine"
  fi

  idx=$(rand_mod 4)
  if [[ "$kind" == "stress" ]]; then
    case "$idx" in
      0) echo "--cores 2 --mem 512M --time 00:06:00" ;;
      1) echo "--cores 3 --mem 768M --time 00:06:45" ;;
      2) echo "--cores 4 --mem 1G --time 00:07:30" ;;
      *) echo "--cores 6 --mem 1536M --time 00:08:30" ;;
    esac
  else
    case "$idx" in
      0) echo "--cores 1 --mem 512M --time 00:07:30" ;;
      1) echo "--cores 2 --mem 768M --time 00:08:00" ;;
      2) echo "--cores 2 --mem 1G --time 00:08:30" ;;
      *) echo "--cores 3 --mem 1G --time 00:09:00" ;;
    esac
  fi
}

pick_user() {
  local idx
  idx=$(rand_mod ${#a_users[@]})
  echo "${a_users[$idx]}"
}

shopt -s nullglob
for script in "$HERE"/*.sh; do
  base=$(basename "$script")
  [[ "$base" == submit-all.sh ]] && continue

  extra=()
  if [[ "$RANDOMIZE" == "1" ]]; then
    # shellcheck disable=SC2206
    profile=( $(profile_for_script "$base") )
    extra+=("${profile[@]}")
    extra+=(--meta "test_mode=multi_user_random")
    extra+=(--meta "test_seed=${SEED}")

    if [[ "$base" != *-user-* ]]; then
      user=$(pick_user)
      extra+=(--meta "research_user=${user}")
    fi
  fi

  echo "=== abc job run ${FLAGS[*]} $script ${extra[*]} ==="
  abc job run "$script" "${extra[@]}" "${FLAGS[@]}"
done
