#!/usr/bin/env bash
# run-grafana-multi-user-burst.sh
#
# Submits overlapping stress-ng (and optionally hyperfine) jobs across real
# research namespaces and IAM principals derived from NS_USERS in
# setup-minio-namespace-buckets.sh. Job names follow script-job-<ns>_<user>--…
# so Grafana usage dashboards can parse research_user from exported_job.
#
# Prerequisites:
#   - abc in PATH; a Nomad token that may submit batch jobs in every target namespace
#   - Each namespace allows containerd-driver (see acl/namespaces/su-*.hcl)
#
# Usage (from repo root = analysis/packages/abc-cluster-cli):
#   export ABC_ACTIVE_CONTEXT=abc-cluster-admin   # preferred; see internal/config/config.go
#   bash deployments/abc-nodes/nomad/tests/workloads/run-grafana-multi-user-burst.sh
#
# Env:
#   ABC_ACTIVE_CONTEXT           Overrides active_context from ~/.abc/config.yaml (preferred).
#   ABC_CONTEXT                  Alias: if set and ABC_ACTIVE_CONTEXT is empty, exported as ABC_ACTIVE_CONTEXT.
#   ABC_BURST_INCLUDE_HYPERFINE  1 (default) or 0
#   ABC_BURST_STRESS_TIME        Walltime for abc --time (default 00:15:00)
#   ABC_BURST_NAME_TAG           Middle fragment in job --name (default grafana-burst)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# workloads → tests → nomad → abc-nodes → deployments → repo root (5 levels)
ROOT="$(cd "${HERE}/../../../../.." && pwd)"
SETUP="${ROOT}/deployments/abc-nodes/acl/setup-minio-namespace-buckets.sh"
STRESS="${HERE}/stress-ng-cpu-default.sh"
HF="${HERE}/hyperfine-micro-default.sh"

INCLUDE_HF="${ABC_BURST_INCLUDE_HYPERFINE:-1}"
WALLTIME="${ABC_BURST_STRESS_TIME:-00:15:00}"
TAG="${ABC_BURST_NAME_TAG:-grafana-burst}"

# The Go CLI only reads ABC_ACTIVE_CONTEXT (not ABC_CONTEXT). Accept ABC_CONTEXT here for convenience.
if [[ -n "${ABC_CONTEXT:-}" && -z "${ABC_ACTIVE_CONTEXT:-}" ]]; then
  export ABC_ACTIVE_CONTEXT="${ABC_CONTEXT}"
fi

# Catch common typo "boostrap" (missing "t") in the context name that will actually apply.
effective_ctx="${ABC_ACTIVE_CONTEXT:-${ABC_CONTEXT:-}}"
if [[ -n "${effective_ctx}" && "${effective_ctx}" == *boostrap* ]]; then
  echo "error: context name looks misspelled (${effective_ctx}); did you mean abc-bootstrap (or aither-bootstrap)?" >&2
  exit 1
fi

if ! command -v abc >/dev/null 2>&1; then
  echo "error: abc CLI not in PATH" >&2
  exit 1
fi

if [[ ! -f "${SETUP}" ]]; then
  echo "error: missing ${SETUP}" >&2
  exit 1
fi

if [[ ! -f "${STRESS}" ]]; then
  echo "error: missing ${STRESS}" >&2
  exit 1
fi

declare -a PAIRS=()
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  ns="${line%%|*}"
  user="${line#*|}"
  PAIRS+=( "${ns}|${user}" )
done < <(python3 - "$SETUP" <<'PY'
import re
import sys
from pathlib import Path

if len(sys.argv) < 2:
    raise SystemExit("internal error: setup script path missing (argv too short)")
setup = Path(sys.argv[1])
if not setup.is_file():
    raise SystemExit(f"setup script not found: {setup}")
text = setup.read_text(encoding="utf-8")
# Allow optional leading whitespace (editor / copy differences).
pat = re.compile(r'^\s*NS_USERS\["([^"]+)"\]="([^"]*)"\s*$', re.M)
for m in pat.finditer(text):
    ns = m.group(1)
    for u in m.group(2).split(","):
        u = u.strip()
        if u:
            print(f"{ns}|{u}")
PY
)

if [[ ${#PAIRS[@]} -eq 0 ]]; then
  echo "error: no NS_USERS entries parsed from ${SETUP}" >&2
  exit 1
fi

echo "==> Parsed ${#PAIRS[@]} namespace/user pair(s) from NS_USERS"

pids=()
submit_one() {
  local ns="$1"
  local user="$2"
  local kind="$3"
  local script="$4"
  local cores="$5"
  local mem="$6"
  local principal="${ns}_${user}"
  local name="${principal}--${TAG}-${kind}"

  echo "=== abc job run ${script} --submit namespace=${ns} name=${name} cores=${cores} ==="
  abc job run "${script}" --submit \
    --namespace "${ns}" \
    --name "${name}" \
    --meta "research_user=${principal}" \
    --meta "workload=${kind}" \
    --meta "scenario=grafana_multi_user_burst" \
    --cores "${cores}" \
    --mem "${mem}" \
    --time "${WALLTIME}" &
  pids+=($!)
}

for pair in "${PAIRS[@]}"; do
  ns="${pair%%|*}"
  user="${pair#*|}"
  # Spread cores a bit to mimic mixed cluster load (deterministic from names)
  stress_cores=$((2 + (${#ns} + ${#user}) % 4))
  submit_one "${ns}" "${user}" "stress-ng" "${STRESS}" "${stress_cores}" "768M"
  if [[ "${INCLUDE_HF}" == "1" ]]; then
    hf_cores=$((1 + (${#ns} + ${#user}) % 2))
    submit_one "${ns}" "${user}" "hyperfine" "${HF}" "${hf_cores}" "512M"
  fi
done

rc=0
for pid in "${pids[@]}"; do
  if ! wait "${pid}"; then
    rc=1
  fi
done

if [[ "${rc}" -ne 0 ]]; then
  echo "error: one or more submissions failed (check Nomad ACL or namespace driver caps)" >&2
  exit "${rc}"
fi

echo "==> All submissions finished. Validate Grafana / Prometheus (see docs/abc-nodes-observability-and-operations.md)."
