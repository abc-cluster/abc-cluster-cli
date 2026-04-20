# Policy: admin
# Full cluster management.  Attach only to operator/admin tokens.
#
# Apply:
#   nomad acl policy apply -description "Full cluster admin" admin acl/policies/admin.hcl

namespace "*" {
  policy = "write"
  capabilities = [
    "alloc-exec",
    "alloc-lifecycle",
    "alloc-node-exec",
    "csi-list-volume",
    "csi-mount-volume",
    "csi-read-volume",
    "csi-register-plugin",
    "csi-write-volume",
    "dispatch-job",
    "list-jobs",
    "list-scaling-policies",
    "read-fs",
    "read-job",
    "read-logs",
    "read-scaling-policy",
    "scale-job",
    "sentinel-override",
    "submit-job",
  ]
}

node {
  policy = "write"
}

agent {
  policy = "write"
}

operator {
  policy = "write"
}

plugin {
  policy = "read"
}
