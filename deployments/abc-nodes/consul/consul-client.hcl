# Consul CLIENT configuration — abc-nodes worker nodes (node2, node3, …)
#
# Topology
# ─────────
#  Client agents register local Nomad allocations into the Consul catalog.
#  They do NOT participate in Raft consensus — the single server on aither
#  handles that. All service health checks are evaluated locally by each client.
#
# Networking
# ──────────
#  Replace NODE_TAILSCALE_IP with this node's Tailscale IP before deploying.
#  bind_addr / advertise_addr bind on the Tailscale interface so gossip stays
#  on the private tailnet. The server (aither) joins at 100.70.185.46.
#
# DNS
# ───
#  dnsmasq must be installed on every client node (join-node.sh handles this).
#  Consul DNS on port 8600 is reached via dnsmasq exactly as on aither.
#
# Gossip Encryption
# ─────────────────
#  Must match the encrypt key in consul.hcl (server). Generate once:
#    consul keygen
#  Set the same value here and in consul.hcl before deploying any client.

datacenter = "dc1"
data_dir   = "/opt/consul"

server = false  # client agent, not a server

# Replace with this node's Tailscale IP.
bind_addr      = "NODE_TAILSCALE_IP"
client_addr    = "127.0.0.1 NODE_TAILSCALE_IP"
advertise_addr = "NODE_TAILSCALE_IP"

# Join the Consul server on aither via Tailscale.
retry_join = ["100.70.185.46"]

# Gossip encryption — must match consul.hcl on the server.
# Uncomment after generating the key with `consul keygen`.
# encrypt = "REPLACE_WITH_SAME_KEY_AS_SERVER"

ports {
  http = 8500
  dns  = 8600
}

log_level = "INFO"

# ACL disabled — matches server config. Enable both together during Phase-3.
acl {
  enabled = false
}
