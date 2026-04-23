namespace "su-sdsct-ceri" {
  capabilities = [
    "submit-job",
    "parse-job",
    "dispatch-job",
    "list-jobs",
    "read-job",
    "read-logs",
    "read-fs",
    "alloc-lifecycle",
    "alloc-exec",
  ]
}

namespace "default" {
  capabilities = [
    "parse-job",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
