#!/usr/bin/env bash
# submit-hello-world.sh
#
# Quick smoke test for CLI-based submission using a tiny hello-world batch job.
# Intended for validating user contexts/tokens with minimal cluster load.
#
# Usage:
#   bash deployments/abc-nodes/nomad/tests/workloads/submit-hello-world.sh
#   ABC_ACTIVE_CONTEXT=su-mbhg-hostgen_dayna \
#   ABC_HELLO_NAMESPACE=su-mbhg-hostgen \
#   bash deployments/abc-nodes/nomad/tests/workloads/submit-hello-world.sh
#
# Env:
#   ABC_ACTIVE_CONTEXT  Preferred active context override.
#   ABC_CONTEXT         Alias accepted if ABC_ACTIVE_CONTEXT is unset.
#   ABC_HELLO_NAMESPACE Nomad namespace override (default: default).
#   ABC_HELLO_NAME      Job name override (default: wl-hello-world-<namespace>-<epoch>).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${HERE}/hello-world-default.sh"
NS="${ABC_HELLO_NAMESPACE:-default}"
NAME="${ABC_HELLO_NAME:-wl-hello-world-${NS}-$(date +%s)}"

if [[ -n "${ABC_CONTEXT:-}" && -z "${ABC_ACTIVE_CONTEXT:-}" ]]; then
  export ABC_ACTIVE_CONTEXT="${ABC_CONTEXT}"
fi

if ! command -v abc >/dev/null 2>&1; then
  echo "error: abc CLI not in PATH" >&2
  exit 1
fi

if [[ ! -f "${SCRIPT}" ]]; then
  echo "error: missing ${SCRIPT}" >&2
  exit 1
fi

echo "=== abc job run ${SCRIPT} --namespace ${NS} --name ${NAME} --submit --watch ==="
abc job run "${SCRIPT}" \
  --namespace "${NS}" \
  --name "${NAME}" \
  --meta "research_user=${ABC_ACTIVE_CONTEXT:-unknown-context}" \
  --submit \
  --watch
