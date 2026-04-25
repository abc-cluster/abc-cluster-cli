# HashiCorp Vault — server configuration
# Deployed to /etc/vault.d/vault.hcl by deploy-vault.sh
#
# This file contains NO secrets.
# Unseal keys are loaded from /etc/vault.d/vault.env (chmod 600, root:root)
# via systemd EnvironmentFile= and consumed by /etc/vault.d/unseal.sh.
#
# Static config: no Nomad template syntax, no env var references.
# All values that change per deployment are overridden via deploy-vault.sh vars.

disable_mlock = true
ui            = true

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 1
}

storage "raft" {
  # Canonical data path for the systemd-managed service.
  # deploy-vault.sh migrates /opt/nomad/vault/data → /opt/vault/data if needed.
  path    = "/opt/vault/data"
  node_id = "abc-nodes-vault-1"
}

# Tailscale IP — stable across reboots, reachable from all cluster nodes.
# This is the single-node aither address; update if the cluster grows.
api_addr     = "http://100.70.185.46:8200"
cluster_addr = "http://100.70.185.46:8201"
