# oci-eu — first non-South-African node in seedling-prod, provisioned 2026-07-09
# from a fully bare OCI VM (no Tailscale/Docker/Nomad pre-installed, unlike the
# oci-af replacement images this cluster has seen so far).
#
# Host: OCI VM oci-eu-lowspec-20260709061311, 18 vCPU (9 OCPU) / 35 GiB RAM,
# Ubuntu, kernel 6.17.0-1011-oracle, OCI eu-frankfurt-1 — GENUINELY EU-hosted,
# GDPR jurisdiction. This is materially different from oci-af (commercial
# cloud VM, but still physically African) — it's the cluster's first real
# cross-border-from-SA jurisdiction boundary.
#
# ⚠ SOVEREIGNTY GAP, flagged explicitly (raised before provisioning, decision
# made 2026-07-09): there is no automated Nomad-level enforcement yet
# preventing POPIA-governed workloads from being scheduled here — the Jurist
# admission-controller mechanism for jurisdiction-based placement constraints
# is still `design/exploring/abc-sovereignty-jurisdiction-model.md` in
# abc-universe (not built), and every node in this cluster — including this
# one — shares a single flat Nomad `datacenter = "seedling-prod"` string with
# no per-jurisdiction differentiation. Decided to join the SAME `compute`
# node_pool as nomad01-03/oci-af rather than a separate `compute-eu` pool
# (the safer alternative, which would have required an explicit
# --worker-pool opt-in for anything landing here) — meaning any pipeline run
# without an explicit worker-pool constraint COULD land real data-processing
# work on EU soil. Do not route real POPIA-governed workloads here on the
# assumption that something will stop you — nothing currently does.
#
# node_pool = "compute" (joins nomad01/02/03, oci-af) per the above decision.
#
# Firewall: same restrictive INPUT-chain pattern seen on oci-af's replacement
# boxes (only lo/established/ssh accepted, default REJECT) — proactively
# fixed here BEFORE first pipeline test, unlike oci-af where it was found
# live via a hung worker-registration callback. See brainstorms/abc-data-
# node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md in abc-universe
# for the original incident this preempts.
#
# nomad-driver-exec2 installed proactively too (binary + plugin_dir copied
# from oci-af's own copy) — see brainstorms/abc-data-node/2026-07-08-exec-
# driver-hang-nomad01-03-kernel-5.15.md for why this matters on some nodes
# (kernel 5.15/landlock v1). This node's kernel (6.17, same generation as
# oci-af's) isn't expected to hit that hang, but exec2 is installed anyway
# for parity/safety rather than waiting to find out live.
#
# Single 46 GB root disk only (no separate large data disk like oci-af's
# mbovis attachment) — host volumes live under /opt/abc-seedling/ on that
# disk, not a dedicated mount. nf-work/abc-tools populated 2026-07-09 by
# copying the s5cmd binary directly from aither's own copy — no automated
# abc-tools-sync mechanism exists cluster-wide (same gap noted on every
# other node's reference config).
plugin_dir = "/opt/nomad/plugins"
datacenter = "seedling-prod"
data_dir   = "/opt/nomad"
log_level  = "INFO"
name       = "oci-eu"

bind_addr = "100.113.83.114"
advertise {
  http = "100.113.83.114"
  rpc  = "100.113.83.114"
  serf = "100.113.83.114"
}

client {
  enabled           = true
  servers           = ["100.70.185.46:4647"]
  network_interface = "tailscale0"
  node_pool         = "compute"
  node_class        = "standard"

  # Mirrors nomad01/02/03/oci-af's alloc-retention bump (server.hcl's
  # job_gc_threshold counterpart) — see aither-client.hcl for the incident
  # this fixed.
  gc_max_allocs            = 5000
  gc_disk_usage_threshold  = 85
  gc_inode_usage_threshold = 85

  meta {
    "node.locality.site"          = "frankfurt"
    "node.locality.network"       = "tailscale"
    "node.locality.datacenter"    = "seedling-prod"
    "node.hardware.cpus"          = "18"
    "node.hardware.mem_gb"        = "35"
    "node.hardware.gpus"          = "0"
    "node.hardware.infiniband"    = "false"
    "node.os.name"                = "ubuntu"
    "node.os.version"             = "24.04"
    "node.software.apptainer"     = ""
    "node.software.cuda"          = ""
    "node.software.s5cmd"         = "2.3.0"
    "node.capability.exec2"       = "true"
    "node.capability.podman"      = "false"
    "node.capability.singularity" = "false"
    "node.capability.docker"      = "true"
    "node.capability.containerd"  = "false"
    "node.compliance.popia"       = "false"
    "node.compliance.gdpr"        = "true"
    "node.compliance.kenya_dpa"   = "false"
    "node.workload.groups"        = "open"
  }

  host_volume "scratch" {
    path      = "/opt/abc-seedling/scratch"
    read_only = false
  }
  host_volume "nf-work" {
    path      = "/opt/abc-seedling/nf-work"
    read_only = false
  }
  host_volume "abc-tools" {
    path      = "/opt/abc-seedling/abc-tools"
    read_only = true
  }
}

acl {
  enabled = true
}

plugin "docker" {
  config {
    allow_privileged = true
    volumes {
      enabled = true
    }
  }
}
plugin "raw_exec" {
  config { enabled = true }
}
plugin "nomad-driver-exec2" {
  config {
    unveil_defaults = true
    unveil_paths    = []
    unveil_by_task  = false
  }
}
