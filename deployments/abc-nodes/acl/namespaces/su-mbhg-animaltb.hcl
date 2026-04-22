name        = "su-mbhg-animaltb"
description = "SU-MBHG Animal TB — pipelines and ad-hoc batch jobs"

capabilities {
  enabled_task_drivers  = ["containerd-driver", "docker", "exec"]
  disabled_task_drivers = ["raw_exec"]
}

meta {
  group        = "su-mbhg-animaltb"
  priority     = "normal"
  job_priority = "50"
  s3_bucket    = "su-mbhg-animaltb"
}
