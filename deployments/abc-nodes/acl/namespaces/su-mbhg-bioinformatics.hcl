# Namespace: su-mbhg-bioinformatics
# Research group: SU-MBHG Bioinformatics (HIGH scheduling priority).
# Members run both interactive batch jobs and Nextflow pipelines.
#
# Apply:  nomad namespace apply -f acl/namespaces/su-mbhg-bioinformatics.hcl

name        = "su-mbhg-bioinformatics"
description = "SU-MBHG Bioinformatics — pipelines and ad-hoc batch jobs (high priority)"

# Allow docker and exec; raw_exec disabled for group members.
# The group-admin policy re-enables raw_exec for emergency use.
capabilities {
  enabled_task_drivers  = ["containerd-driver", "docker", "exec"]
  disabled_task_drivers = ["raw_exec"]
}

meta {
  group        = "su-mbhg-bioinformatics"
  priority     = "high"
  job_priority = "70"
  contact      = "bioinformatics-pi@su-mbhg.example.edu"
  s3_bucket    = "su-mbhg-bioinformatics"
  ntfy_topic   = "su-mbhg-bioinformatics-jobs"
}
