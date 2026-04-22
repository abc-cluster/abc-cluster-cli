#!/usr/bin/env bash
set -euo pipefail

# Idempotently create/update Nomad ACL roles that map onto existing policies.
# Default context is the bootstrap admin context used elsewhere in this repo.

if [[ -n "${ABC_ACTIVE_CONTEXT:-}" ]]; then
  export ABC_ACTIVE_CONTEXT
fi

NOMAD_CLI=(abc admin services nomad cli --)

GROUP_NAMESPACES=(
  "su-mbhg-animaltb"
  "su-mbhg-bioinformatics"
  "su-mbhg-hostgen"
  "su-mbhg-tbgenomics"
  "su-psy-neuropsychiatry"
  "su-sdsct-ceri"
)

usage() {
  cat <<'EOF'
Usage: ./roles/apply-roles.sh [--print-migration-commands]

Creates/updates:
  - role-cluster-admin
  - role-platform-services-operator
  - role-platform-applications-operator
  - role-observer-readonly
  - role-group-admin-<namespace> (for each research namespace)
  - role-group-member-<namespace> (for each research namespace)

Options:
  --print-migration-commands  Print optional token-to-role migration commands.
EOF
}

run_nomad() {
  "${NOMAD_CLI[@]}" "$@"
  local rc=$?
  if [[ ${rc} -ne 0 ]]; then
    echo "run nomad [$*]: exit status ${rc}" >&2
  fi
  return "${rc}"
}

ensure_role() {
  local role_name="$1"
  local description="$2"
  shift 2
  local policies=("$@")

  local role_id
  local roles_json
  roles_json="$(run_nomad acl role list -json)"

  role_id="$(printf '%s\n' "${roles_json}" | python3 -c '
import json, sys
roles = json.load(sys.stdin)
target = sys.argv[1]
for role in roles:
    if role.get("Name") == target:
        print(role.get("ID", ""))
        break
' "${role_name}")"

  local policy_flags=()
  local p
  for p in "${policies[@]}"; do
    policy_flags+=("-policy=${p}")
  done

  if [[ -n "${role_id}" ]]; then
    echo "Updating role ${role_name} (${role_id})"
    run_nomad acl role update \
      -name "${role_name}" \
      -description "${description}" \
      -no-merge \
      "${policy_flags[@]}" \
      "${role_id}" >/dev/null
  else
    echo "Creating role ${role_name}"
    run_nomad acl role create \
      -name "${role_name}" \
      -description "${description}" \
      "${policy_flags[@]}" >/dev/null
  fi
}

print_migration_commands() {
  cat <<'EOF'
# Optional: migrate existing tokens from direct policies to role bindings.
# 1) Inspect current tokens:
#    abc admin services nomad cli -- acl token list
#
# 2) For each token, replace direct policies with role IDs:
#    abc admin services nomad cli -- acl token update \
#      -id <ACCESSOR_ID> \
#      -name <TOKEN_NAME> \
#      -type client \
#      -role-id <ROLE_ID_1> \
#      -role-id <ROLE_ID_2> \
#      -no-merge
#
# 3) Resolve role IDs:
#    abc admin services nomad cli -- acl role list
EOF
}

main() {
  local print_migration="false"
  if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    usage
    exit 0
  fi
  if [[ "${1:-}" == "--print-migration-commands" ]]; then
    print_migration="true"
  elif [[ -n "${1:-}" ]]; then
    echo "Unknown option: ${1}" >&2
    usage >&2
    exit 1
  fi

  if [[ -n "${ABC_ACTIVE_CONTEXT:-}" ]]; then
    echo "Using ABC_ACTIVE_CONTEXT=${ABC_ACTIVE_CONTEXT}"
  else
    echo "Using current abc CLI context"
  fi
  echo "Applying base roles..."

  ensure_role "role-cluster-admin" \
    "Cluster-wide admin role for all Nomad resources." \
    "admin"

  ensure_role "role-platform-services-operator" \
    "Operator role for platform services in abc-services namespace." \
    "services-admin"

  ensure_role "role-platform-applications-operator" \
    "Operator role for shared apps in abc-applications namespace." \
    "applications-admin"

  ensure_role "role-observer-readonly" \
    "Read-only observer across cluster and namespaces." \
    "observer"

  echo "Applying research group roles..."
  local ns
  for ns in "${GROUP_NAMESPACES[@]}"; do
    ensure_role "role-group-admin-${ns}" \
      "Group admin for ${ns} namespace." \
      "${ns}-group-admin"

    ensure_role "role-group-member-${ns}" \
      "Group member for ${ns} namespace." \
      "${ns}-member"
  done

  echo
  echo "Final role list:"
  run_nomad acl role list

  if [[ "${print_migration}" == "true" ]]; then
    echo
    print_migration_commands
  fi
}

main "$@"
