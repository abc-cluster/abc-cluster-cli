# oci-af (af-ubuntu2404) — joining seedling-prod as a compute-pool client.
#
# Previously configured (2026-03-04, add-oci-af-nomad-client.ps1) as a client of a
# now-dead cluster ("oci-nomadlab": nomad00/nomad01/oci-abhi-phd-arm-sa as servers,
# all offline per `tailscale status`). Re-pointed 2026-07-05 to seedling-prod
# (servers = aither's Tailscale IP, matching every other compute node's client
# config) to add extra compute capacity for pipeline-scale testing.
#
# Host: OCI VM af-ubuntu2404, 32 vCPU / 31 GiB RAM, Ubuntu 24.04, af-jhb
# (Johannesburg, South Africa) region — same jurisdiction as the SU-hosted
# physical fleet (aither/nomad01-03, Stellenbosch), hence popia=true below,
# but this is a commercial cloud VM, not a university-audited HPC node — no
# separate data-processor agreement has been reviewed. Flag before routing any
# real (non-test) POPIA-governed data here; fine for pipeline-capacity testing.
#
# node_pool = "compute" (joins nomad01/02/03, not a separate pool) — decided
# 2026-07-05. Scratch host_volume backed by the mbovis disk
# (/home/ubuntu/data-volumes/data-mbovis-1tb-01, 1 TB, 623G free at setup time)
# per the same request.
#
# No apptainer/singularity, no nomad-driver-exec2, no containerd Nomad plugin,
# no podman installed on this host (verified live) — only docker + raw_exec
# capabilities are enabled below; don't copy capability flags from other nodes
# without verifying what's actually installed here.
#
# nf-work host_volume: freshly created at /home/ubuntu/abc-seedling/nf-work
# (this host has no /opt/abc-seedling), with s5cmd v2.3.0-991c9fb copied from
# aither's own binary (matches the cluster-pinned version exactly) at
# bin/s5cmd — needed for the S3-workdir worker mount fix (abc-cluster-cli
# PR #37, fix/s3-workdir-nf-work-carrier) to actually work on this node.
# abc-tools (ADR-0061): manually populated 2026-07-05 by copying the same
# s5cmd binary (bin/s5cmd, root:root 0755) from aither's own abc-tools dir —
# there is still no automated abc-tools-sync mechanism anywhere in the
# cluster (see brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-
# worker-mount-report.md's "abc-tools-sync is NOT verified in code" finding),
# so this copy will silently drift from aither's/nomad01-03's if abc-tools'
# contents ever change and this node isn't manually updated too. Registered
# read-only, matching every other compute node.
datacenter = "seedling-prod"
data_dir   = "/opt/nomad"
log_level  = "INFO"
name       = "oci-af"

bind_addr = "100.89.64.44"
advertise {
  http = "100.89.64.44"
  rpc  = "100.89.64.44"
  serf = "100.89.64.44"
}

client {
  enabled           = true
  servers           = ["100.70.185.46:4647"]
  network_interface = "tailscale0"
  node_pool         = "compute"
  node_class        = "standard"

  # Mirrors nomad01/02/03's alloc-retention bump (server.hcl's job_gc_threshold
  # counterpart) — see aither-client.hcl for the incident this fixed.
  gc_max_allocs            = 5000
  gc_disk_usage_threshold  = 85
  gc_inode_usage_threshold = 85

  meta {
    "node.locality.site"          = "johannesburg"
    "node.locality.network"       = "tailscale"
    "node.locality.datacenter"    = "seedling-prod"
    "node.hardware.cpus"          = "32"
    "node.hardware.mem_gb"        = "31"
    "node.hardware.gpus"          = "0"
    "node.hardware.infiniband"    = "false"
    "node.os.name"                = "ubuntu"
    "node.os.version"             = "24.04"
    "node.software.apptainer"     = ""
    "node.software.cuda"          = ""
    "node.software.s5cmd"         = "2.3.0"
    "node.capability.exec2"       = "false"
    "node.capability.podman"      = "false"
    "node.capability.singularity" = "false"
    "node.capability.docker"      = "true"
    "node.capability.containerd"  = "false"
    "node.compliance.popia"       = "true"
    "node.compliance.gdpr"        = "false"
    "node.compliance.kenya_dpa"   = "false"
    "node.workload.groups"        = "open"
  }

  # mbovis disk (1 TB block volume, /dev/sdb) as pipeline worker scratch space,
  # per 2026-07-05 request. NOT the vinotype disk (/dev/sdc) — that's a
  # separate, unrelated dataset, left untouched. Scoped to a fresh
  # _scratch/nomad-pipeline-scratch/ subdirectory, not the disk root, for two
  # reasons found live: (1) Docker's own daemon root is nested at
  # <disk>/DOCKER_ROOT/docker — bind-mounting an ancestor of Docker's storage
  # root fails ("must use either propagation mode rslave or rshared"); (2) the
  # disk root and its existing _scratch/ dir already hold 17G+ of the owner's
  # own prior manual mbovis pipeline runs (work/, .nextflow.log*, TBtypeR/,
  # _deleteme/) — a fresh subdirectory keeps Nomad-submitted job scratch from
  # comingling with that pre-existing research data.
  host_volume "scratch" {
    path      = "/home/ubuntu/data-volumes/data-mbovis-1tb-01/_scratch/nomad-pipeline-scratch"
    read_only = false
  }
  host_volume "nf-work" {
    path      = "/home/ubuntu/abc-seedling/nf-work"
    read_only = false
  }
  host_volume "abc-tools" {
    path      = "/home/ubuntu/abc-seedling/abc-tools"
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
