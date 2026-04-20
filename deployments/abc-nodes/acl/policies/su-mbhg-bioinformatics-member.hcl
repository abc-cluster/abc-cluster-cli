# Policy: su-mbhg-bioinformatics-member
# Standard researcher token for SU-MBHG Bioinformatics members.
# Can submit and manage their OWN jobs; cannot see or cancel others' jobs
# (Nomad OSS does not enforce per-user job ownership at the ACL level —
#  honour system + group-admin oversight).
# Can read logs and alloc filesystem for any job in the namespace.
#
# Apply:
#   nomad acl policy apply \
#     -description "Member — SU-MBHG Bioinformatics" \
#     su-mbhg-bioinformatics-member \
#     acl/policies/su-mbhg-bioinformatics-member.hcl

namespace "su-mbhg-bioinformatics" {
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
