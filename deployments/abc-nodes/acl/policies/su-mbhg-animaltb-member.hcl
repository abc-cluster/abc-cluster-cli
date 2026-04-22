namespace "su-mbhg-animaltb" {
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
