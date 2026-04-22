# Namespace: su-mbhg-hostgen
# Research group: SU-MBHG Host Genetics (NORMAL scheduling priority).
# Members run both interactive batch jobs and Nextflow pipelines.
#
# Apply:  nomad namespace apply -f acl/namespaces/su-mbhg-hostgen.hcl

name        = "su-mbhg-hostgen"
description = "SU-MBHG Host Genetics — pipelines and ad-hoc batch jobs (normal priority)"

capabilities {
  enabled_task_drivers  = ["containerd-driver", "docker", "exec"]
  disabled_task_drivers = ["raw_exec"]
}

meta {
  group        = "su-mbhg-hostgen"
  priority     = "normal"
  job_priority = "50"
  contact      = "hostgen-pi@su-mbhg.example.edu"
  s3_bucket    = "su-mbhg-hostgen"
  ntfy_topic   = "su-mbhg-hostgen-jobs"
}
