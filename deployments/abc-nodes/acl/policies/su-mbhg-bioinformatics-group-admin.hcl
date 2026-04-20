# Policy: su-mbhg-bioinformatics-group-admin
# Group administrator for SU-MBHG Bioinformatics.
# Can do everything in the group namespace including cancelling other members' jobs,
# reading all allocations, and submitting raw_exec tasks for debugging.
# Cannot touch other namespaces or cluster-level operator/agent config.
#
# Apply:
#   nomad acl policy apply \
#     -description "Group admin — SU-MBHG Bioinformatics" \
#     su-mbhg-bioinformatics-group-admin \
#     acl/policies/su-mbhg-bioinformatics-group-admin.hcl

namespace "su-mbhg-bioinformatics" {
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

# Group admin can see nodes to diagnose placement failures.
node  { policy = "read" }
agent { policy = "read" }
