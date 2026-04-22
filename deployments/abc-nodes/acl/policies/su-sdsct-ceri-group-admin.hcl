namespace "su-sdsct-ceri" {
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
