# oci-af — REPLACEMENT box, provisioned 2026-07-06, superseding the original oci-af
# (af-ubuntu2404, decommissioned after an operator error wiped ~/home including
# ~/.ssh on that host, breaking SSH access irrecoverably). Same Tailscale identity
# carried over (oci-af-client-01 @ 100.89.64.44), same mbovis 1TB data disk
# attached at the same host path, but the disk contents themselves are a clean
# slate — nothing from the old box's directory layout survived, and nothing
# should be assumed to.
#
# Host specs differ from the original: 8 vCPU / 62 GiB RAM (was 32 vCPU / 31 GiB),
# Ubuntu 24.04, OCI af-jhb (Johannesburg, South Africa) — same jurisdiction as the
# SU-hosted physical fleet (aither/nomad01-03, Stellenbosch), hence popia=true
# below, but this is a commercial cloud VM, not a university-audited HPC node —
# no separate data-processor agreement has been reviewed. Flag before routing any
# real (non-test) POPIA-governed data here; fine for pipeline-capacity testing.
#
# node_pool = "compute" (joins nomad01/02/03), node `name` kept as "oci-af" for
# continuity with existing docs/PRs even though the OS hostname is now
# "oci-af-replacement-20260706194142" — be aware abc-cluster-cli's --node flag
# uses INCONSISTENT attributes for head vs. --pin-workers placement (OS hostname
# vs. this configured name); see brainstorms/abc-data-node/2026-07-04-aither-
# abc-tools-rw-worker-mount-report.md in abc-universe and the open follow-up
# task for that bug. Use the node-eligibility-toggle method for single-node
# testing until it's fixed, not --node/--pin-workers.
#
# Docker data-root: ALREADY on the mbovis disk on this box out of the gate
# (`Docker Root Dir: .../data-mbovis-1tb-01/DOCKER_ROOT/docker`, verified via
# `docker info` — apparently baked into the provisioning image/snapshot this
# replacement came from) — for longevity/capacity, per 2026-07-06 request.
# Left as-is; not reconfigured here.
#
# Host-volume layout: everything Nomad-managed lives under a fresh
# abc-nomad/{scratch,nf-work,abc-tools} tree, as a SIBLING of DOCKER_ROOT/ on the
# same disk — NOT nested inside or an ancestor of it. This is deliberate: on the
# original oci-af, Docker's daemon root was nested INSIDE the registered
# `scratch` host_volume's path, and bind-mounting an ancestor of Docker's own
# storage root failed ("must use either propagation mode rslave or rshared").
# Keeping Nomad's tree and DOCKER_ROOT as siblings avoids that class of bug
# entirely, rather than working around it with a deeper subdirectory.
#
# nf-work/abc-tools populated 2026-07-06 by copying the same s5cmd binary
# (v2.3.0-991c9fb, root:root 0755) from aither's own copy — there is still no
# automated abc-tools-sync mechanism anywhere in the cluster (see the report
# above, "abc-tools-sync is NOT verified in code"), so these copies will
# silently drift from aither's/nomad01-03's if abc-tools' contents ever change
# and this node isn't manually updated too.
#
# The docker0->host-Tailscale-IP iptables ACCEPT rule (needed for nf-nomad's
# worker-registration callback to the node-local Nomad agent) was ALREADY
# present on this box at first boot (verified via `iptables -L INPUT -v`) —
# presumably baked into the same provisioning image/snapshot as the Docker
# data-root setting. Left as-is; not reapplied here. If a future replacement
# doesn't have it, see the report above for the exact rule
# (`-i docker0 -p tcp --dport 4646 -j ACCEPT`, persisted via iptables-persistent).
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
    "node.hardware.cpus"          = "8"
    "node.hardware.mem_gb"        = "62"
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

  host_volume "scratch" {
    path      = "/home/ubuntu/data-volumes/data-mbovis-1tb-01/abc-nomad/scratch"
    read_only = false
  }
  host_volume "nf-work" {
    path      = "/home/ubuntu/data-volumes/data-mbovis-1tb-01/abc-nomad/nf-work"
    read_only = false
  }
  host_volume "abc-tools" {
    path      = "/home/ubuntu/data-volumes/data-mbovis-1tb-01/abc-nomad/abc-tools"
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
