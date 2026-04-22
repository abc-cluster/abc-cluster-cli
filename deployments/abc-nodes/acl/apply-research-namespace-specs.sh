#!/usr/bin/env bash
# apply-research-namespace-specs.sh
#
# Applies Nomad namespace definitions for research groups (files matching su-*.hcl).
# Skips platform namespaces (abc-*.hcl) so abc-services / abc-applications are not
# touched by this script.
#
# Usage (from repo root = analysis/packages/abc-cluster-cli):
#   export ABC_CONTEXT=abc-cluster-admin
#   bash deployments/abc-nodes/acl/apply-research-namespace-specs.sh
#
# Env:
#   NOMAD_CLI  Optional prefix instead of default "abc admin services nomad cli --"
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACES_DIR="${SCRIPT_DIR}/namespaces"
# shellcheck disable=SC2206
NOMAD_CLI=(${NOMAD_CLI:-abc admin services nomad cli --})

if [[ ! -d "${NAMESPACES_DIR}" ]]; then
  echo "error: missing ${NAMESPACES_DIR}" >&2
  exit 1
fi

if ! command -v abc >/dev/null 2>&1; then
  echo "error: abc CLI not in PATH" >&2
  exit 1
fi

shopt -s nullglob
files=( "${NAMESPACES_DIR}"/su-*.hcl )
if [[ ${#files[@]} -eq 0 ]]; then
  echo "error: no su-*.hcl files under ${NAMESPACES_DIR}" >&2
  exit 1
fi

for f in "${files[@]}"; do
  echo "+ ${NOMAD_CLI[*]} namespace apply ${f}"
  "${NOMAD_CLI[@]}" namespace apply "$f"
done

echo "Applied ${#files[@]} research namespace spec(s) from ${NAMESPACES_DIR}/su-*.hcl."
