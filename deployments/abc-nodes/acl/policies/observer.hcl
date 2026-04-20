# Policy: observer
# Read-only access to all namespaces.  Attach to collaborator / advisor tokens.
#
# Apply:
#   nomad acl policy apply -description "Read-only observer" observer acl/policies/observer.hcl

namespace "*" {
  capabilities = [
    "list-jobs",
    "read-job",
    "read-logs",
    "read-fs",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
