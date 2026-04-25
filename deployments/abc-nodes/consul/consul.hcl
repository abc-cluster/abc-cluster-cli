# Consul server configuration — abc-nodes (sun-aither)
#
# Topology
# ─────────
#  Single-node server for now (bootstrap_expect = 1).
#  Phase-2 multi-node: bootstrap_expect stays 1 (server count doesn't change —
#  clients join as agents, not servers). Add client Tailscale IPs to retry_join
#  on each new node's consul-client.hcl; no change needed here.
#
# Networking
# ──────────
#  bind_addr    — Tailscale IP: cluster gossip/RPC stays on the private tailnet.
#  client_addr  — loopback + Tailscale IP: local services (Nomad, Caddy, containers
#                 via host DNS) can reach the HTTP API and DNS on both addresses.
#  advertise_addr — same as bind_addr so peer nodes find this server by Tailscale IP.
#
# DNS
# ───
#  Consul DNS listens on port 8600. dnsmasq (installed by deploy-consul.sh) forwards
#  *.consul → 127.0.0.1:8600 so any process on the host can resolve service addresses
#  using <service-name>.service.consul without pointing directly at port 8600.
#
# Gossip Encryption
# ─────────────────
#  Generate a key ONCE and use the same value on all nodes (server and clients).
#  Generate:  consul keygen
#  Then set the `encrypt` field below and re-deploy with deploy-consul.sh.
#  All nodes must use the same key or gossip will fail.
#
# ACL
# ───
#  Disabled for initial bootstrap. Enable via a separate hardening pass once the
#  service catalog is stable. See: https://developer.hashicorp.com/consul/docs/security/acl

datacenter = "dc1"
data_dir   = "/opt/consul"

server           = true
bootstrap_expect = 1

# Bind Tailscale IP for intra-cluster gossip/RPC.
bind_addr      = "100.70.185.46"
client_addr    = "127.0.0.1 100.70.185.46"
advertise_addr = "100.70.185.46"

# Gossip encryption — generate with `consul keygen` and set the same key on all nodes.
# Uncomment and fill in before adding any client nodes.
# encrypt = "REPLACE_WITH_OUTPUT_OF_consul_keygen"

# Client nodes join by connecting to this server's Tailscale IP.
# No changes needed here when adding clients — configure retry_join in consul-client.hcl.
retry_join = ["100.70.185.46"]

ui_config {
  enabled = true
}

ports {
  # HTTP API: 8500 (default). Do not expose on public interface.
  http = 8500
  # DNS: 8600 to avoid conflict with system port 53. dnsmasq bridges the gap.
  dns  = 8600
}

# Performance: keep raft snappy on a single node.
performance {
  raft_multiplier = 1
}

log_level = "INFO"

# ACL disabled — enable when the service catalog is stable.
acl {
  enabled = false
}
