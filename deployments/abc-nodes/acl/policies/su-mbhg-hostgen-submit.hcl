# Policy: su-mbhg-hostgen-submit
# Service-account policy for the nf-nomad Nextflow plugin.
#
# Apply:
#   nomad acl policy apply \
#     -description "nf-nomad submit — SU-MBHG Host Genetics (normal priority)" \
#     su-mbhg-hostgen-submit \
#     acl/policies/su-mbhg-hostgen-submit.hcl

namespace "su-mbhg-hostgen" {
  capabilities = [
    "submit-job",
    "dispatch-job",
    "read-job",
    "list-jobs",
    "alloc-lifecycle",
    "read-logs",
    "read-fs",
    "alloc-exec",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
