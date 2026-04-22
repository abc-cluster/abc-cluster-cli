name        = "su-sdsct-ceri"
description = "SU-SDSCT CERI — pipelines and ad-hoc batch jobs"

capabilities {
  enabled_task_drivers  = ["containerd-driver", "docker", "exec"]
  disabled_task_drivers = ["raw_exec"]
}

meta {
  group        = "su-sdsct-ceri"
  priority     = "normal"
  job_priority = "50"
  s3_bucket    = "su-sdsct-ceri"
}
