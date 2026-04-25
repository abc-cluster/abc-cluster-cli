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

  variables {
    # Full variable access in abc-services — needed to write job-scoped secrets
    # (e.g. nomad/jobs/abc-nodes-boundary-worker for the worker KMS auth key).
    path "*" {
      capabilities = ["create", "read", "update", "delete", "list", "destroy"]
    }
  }
}

# cluster-admin tokens also need to see nodes and agent info.
node  { policy = "write" }
agent { policy = "write" }
operator { policy = "write" }
plugin { policy = "read" }
