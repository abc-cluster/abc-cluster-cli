namespace "su-psy-neuropsychiatry" {
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
