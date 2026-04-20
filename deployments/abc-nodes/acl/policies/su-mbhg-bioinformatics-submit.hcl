# Policy: su-mbhg-bioinformatics-submit
# Service-account policy for the nf-nomad Nextflow plugin.
# One shared token per group; embedded in the group's nextflow config.
# DO NOT issue this token to individual users.
#
# Apply:
#   nomad acl policy apply \
#     -description "nf-nomad submit — SU-MBHG Bioinformatics (high priority)" \
#     su-mbhg-bioinformatics-submit \
#     acl/policies/su-mbhg-bioinformatics-submit.hcl

namespace "su-mbhg-bioinformatics" {
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
