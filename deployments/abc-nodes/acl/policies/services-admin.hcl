# Policy: services-admin
# Full access to the 'abc-services' namespace where all cluster infrastructure
# jobs live (MinIO, Loki, Grafana, Prometheus, tusd, Vault, Traefik, ntfy,
# Alloy, RustFS, Uppy, Wave, Docker registry, abc-nodes-auth).
# Attach only to cluster-admin tokens — NOT to group tokens.
#
# Apply:
#   nomad acl policy apply -description "Services namespace admin" \
#     services-admin acl/policies/services-admin.hcl

namespace "abc-services" {
  policy = "write"
  capabilities = [
    "alloc-exec",
    "alloc-lifecycle",
    "alloc-node-exec",
    "dispatch-job",
    "list-jobs",
    "read-fs",
    "read-job",
    "read-logs",
    "scale-job",
    "submit-job",
  ]
}

# cluster-admin tokens also need to see nodes and agent info.
node  { policy = "write" }
agent { policy = "write" }
operator { policy = "write" }
plugin { policy = "read" }
