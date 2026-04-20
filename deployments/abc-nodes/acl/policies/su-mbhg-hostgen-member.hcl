# Policy: su-mbhg-hostgen-member
# Standard researcher token for SU-MBHG Host Genetics members.
#
# Apply:
#   nomad acl policy apply \
#     -description "Member — SU-MBHG Host Genetics" \
#     su-mbhg-hostgen-member \
#     acl/policies/su-mbhg-hostgen-member.hcl

namespace "su-mbhg-hostgen" {
  capabilities = [
    "submit-job",
    "dispatch-job",
    "list-jobs",
    "read-job",
    "read-logs",
    "read-fs",
    "alloc-lifecycle",
    "alloc-exec",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
