name        = "su-psy-neuropsychiatry"
description = "SU-PSY Neuropsychiatry — pipelines and ad-hoc batch jobs"

capabilities {
  enabled_task_drivers  = ["containerd-driver", "docker", "exec"]
  disabled_task_drivers = ["raw_exec"]
}

meta {
  group        = "su-psy-neuropsychiatry"
  priority     = "normal"
  job_priority = "50"
  s3_bucket    = "su-psy-neuropsychiatry"
}
