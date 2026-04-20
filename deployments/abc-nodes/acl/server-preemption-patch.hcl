# server-preemption-patch.hcl
#
# Add the default_scheduler_config block inside the existing `server {}` stanza
# in nomad_configs/nomad01.hcl, nomad02.hcl, and nomad03.hcl (all three servers).
#
# This enables batch-job preemption: when a high-priority batch job cannot be
# placed because the cluster is full, Nomad will evict one or more lower-priority
# batch jobs to make room.
#
# Service-job preemption (kicking out long-running services) is an Enterprise
# feature and is NOT enabled here.
#
# Alternatively, apply without touching HCL files (takes effect immediately,
# but is lost on server restart unless also added to HCL):
#
#   nomad operator scheduler set-config \
#     -preempt-batch-scheduler=true \
#     -preempt-sysbatch-scheduler=true
#
# ------------------------------------------------------------------
# Paste the block below INTO the existing server {} stanza:
# ------------------------------------------------------------------

# server {
#   enabled          = true
#   bootstrap_expect = 3
#
#   server_join { ... }
#
#   ### ADD THIS BLOCK ###
#   default_scheduler_config {
#     preemption_config {
#       batch_scheduler_enabled    = true   # Nextflow task jobs (type = "batch")
#       sysbatch_scheduler_enabled = true   # Periodic / benchmark jobs (type = "sysbatch")
#       # service_scheduler_enabled = false # Enterprise only — leave commented out
#     }
#   }
#   ### END OF ADDED BLOCK ###
# }

# ------------------------------------------------------------------
# Priority guide for jobs submitted to sun-nomadlab:
#
#   priority = 90   Infrastructure services (MinIO, Redis) — never preempted
#   priority = 70   High-priority group     (e.g. nf-genomics-lab)
#   priority = 50   Normal-priority group   (e.g. nf-proteomics-lab) — Nomad default
#   priority = 30   Low / shared group      (e.g. nf-shared) — yield to others
#
# Batch preemption fires when a job with priority P cannot be scheduled and
# there exist running batch allocations with priority < P.
# ------------------------------------------------------------------
