#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# Configure aither's containerd to trust the local plain-HTTP OCI registry
# ═══════════════════════════════════════════════════════════════════════════
#
# The cluster-local registry (abc-experimental-docker-registry) listens on
# plain HTTP at <tailscale-ip>:5000.  Containerd's default behaviour is to
# negotiate TLS first; without an explicit `hosts.toml` it will fail any
# pull from this registry with "http: server gave HTTP response to HTTPS
# client".
#
# This one-shot script writes the per-host config and restarts containerd.
# Idempotent — safe to re-run.
#
# Usage:
#   ./scripts/configure-aither-registry-trust.sh
#
# Flags:
#   --host <ssh-host>   SSH alias for the cluster node  (default: sun-aither)
#   --addr <ip:port>    Registry address                (default: 100.70.185.46:5000)
#   --dry-run           Print the commands but don't execute them
#   --revert            Remove the trust config + restart containerd
#
# See also:  deployments/abc-nodes/docs/local-docker-registry.md

set -euo pipefail

SSH_HOST="sun-aither"
REG_ADDR="100.70.185.46:5000"
DRY_RUN=0
REVERT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)    SSH_HOST="$2"; shift 2 ;;
    --addr)    REG_ADDR="$2";  shift 2 ;;
    --dry-run) DRY_RUN=1;       shift ;;
    --revert)  REVERT=1;        shift ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
      exit 0 ;;
    *)
      echo "unknown flag: $1" >&2
      exit 2 ;;
  esac
done

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

CONF_DIR="/etc/containerd/certs.d/${REG_ADDR}"
CONF_FILE="${CONF_DIR}/hosts.toml"

run_remote() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo -e "${YELLOW}[dry-run] ssh ${SSH_HOST} '${1}'${NC}"
  else
    # shellcheck disable=SC2029
    ssh "$SSH_HOST" "$1"
  fi
}

if [[ "$REVERT" -eq 1 ]]; then
  echo -e "${BLUE}== Removing containerd trust for ${REG_ADDR} on ${SSH_HOST} ==${NC}"
  run_remote "sudo rm -rf '${CONF_DIR}' && sudo systemctl restart containerd && echo reverted"
  echo -e "${GREEN}✓ done${NC}"
  exit 0
fi

echo -e "${BLUE}== Configuring containerd trust for ${REG_ADDR} on ${SSH_HOST} ==${NC}"

# 1. Sanity-check the registry actually responds before touching containerd.
echo -e "${BLUE}-- Checking registry handshake at http://${REG_ADDR}/v2/${NC}"
if [[ "$DRY_RUN" -eq 0 ]]; then
  if ! curl -fsS --max-time 5 "http://${REG_ADDR}/v2/" >/dev/null; then
    echo -e "${RED}!! Registry at http://${REG_ADDR}/v2/ is not responding.${NC}" >&2
    echo "   Is abc-experimental-docker-registry running?  Try:" >&2
    echo "     terraform apply -target='nomad_job.docker_registry[0]' -var='enable_docker_registry=true'" >&2
    exit 1
  fi
fi

# 2. Write the hosts.toml.  Use a heredoc piped over ssh so quoting is safe.
HOSTS_TOML=$(cat <<TOML
server = "http://${REG_ADDR}"

[host."http://${REG_ADDR}"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
TOML
)

echo -e "${BLUE}-- Writing ${CONF_FILE}${NC}"
if [[ "$DRY_RUN" -eq 1 ]]; then
  echo -e "${YELLOW}[dry-run] would write:${NC}"
  echo "$HOSTS_TOML" | sed 's/^/  /'
else
  ssh "$SSH_HOST" "sudo mkdir -p '${CONF_DIR}' && sudo tee '${CONF_FILE}' >/dev/null" <<<"$HOSTS_TOML"
fi

# 3. Restart containerd to pick up the new config.
echo -e "${BLUE}-- Restarting containerd${NC}"
run_remote "sudo systemctl restart containerd && sudo systemctl is-active containerd"

# 4. Verify the config is in place.
echo -e "${BLUE}-- Verifying${NC}"
run_remote "cat '${CONF_FILE}'"

printf '\n%b✓ aither now trusts http://%s as a plain-HTTP registry.%b\n\n' "$GREEN" "$REG_ADDR" "$NC"
cat <<EOF
Next:
  1. Configure your laptop's docker daemon for the same registry — see
     deployments/abc-nodes/docs/local-docker-registry.md §1
  2. Push an image:
       docker tag my-app:dev ${REG_ADDR}/my-app:dev
       docker push ${REG_ADDR}/my-app:dev
  3. Reference it in a Nomad jobspec:
       config { image = "${REG_ADDR}/my-app:dev" }

EOF
