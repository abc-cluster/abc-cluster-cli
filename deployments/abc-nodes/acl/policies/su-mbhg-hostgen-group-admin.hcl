# Policy: su-mbhg-hostgen-group-admin
# Group administrator for SU-MBHG Host Genetics.
#
# Apply:
#   nomad acl policy apply \
#     -description "Group admin — SU-MBHG Host Genetics" \
#     su-mbhg-hostgen-group-admin \
#     acl/policies/su-mbhg-hostgen-group-admin.hcl

namespace "su-mbhg-hostgen" {
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

node  { policy = "read" }
agent { policy = "read" }
