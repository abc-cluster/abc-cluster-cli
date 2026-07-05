# aither-client.hcl — reference Nomad config for the aither node.
#
# RESYNCED 2026-07-04 from the live /etc/nomad.d/server.hcl on aither — the
# previous version of this file (dated 2026-05-07) described an entirely
# different, superseded architecture: aither as a pure Nomad *client*
# joining external servers (nomad00/nomad01/oci-abhi-phd-arm-sa) over
# Tailscale MagicDNS, with a single `scratch` host_volume. That topology
# predates the current one and was never updated here as the live
# config evolved — found as a "bonus" gap while investigating an
# unrelated abc-tools RO/RW issue (see
# brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md
# in abc-universe). The live host has run as a **single-node
# server+client** (`bootstrap_expect = 1`) for some time — this file now
# mirrors that, including all 8 host volumes actually registered live
# (the old version only had `scratch`; `abc-tools`, `minio-data`,
# `tusd-data`, `nf-work`, `signup-svc`, `obs-data`, and `homedir-abhi`
# were all missing).
#
# Deploy to /etc/nomad.d/server.hcl on aither and restart nomad:
#
#   scp deployments/abc-nodes/clients/aither-client.hcl aither:/tmp/server.hcl.new
#   ssh aither nomad config validate /tmp/server.hcl.new && \
#     ssh aither 'sudo cp -a /etc/nomad.d/server.hcl /etc/nomad.d/server.hcl.bak-$(date +%Y%m%d%H%M%S) && \
#       sudo cp /tmp/server.hcl.new /etc/nomad.d/server.hcl && \
#       sudo chown root:root /etc/nomad.d/server.hcl && sudo chmod 640 /etc/nomad.d/server.hcl && \
#       sudo systemctl restart nomad'
#
# Restart consequence: verified clean historically (2026-05-19) and again
# 2026-07-04 (adding the `scratch` volume) — all allocations on this
# single node (currently dozens: abc-minio, tusd, traefik, caddy,
# grafana, victoriametrics/logs, the crypticdb-llm app trio, etc.) stay
# `running` across a `systemctl restart nomad`; only the Nomad agent
# process restarts, not the Docker containers it supervises.
#
# This file is checked in as reference only — it is NOT deployed
# automatically by a Nomad job or Terraform. Placement is manual (see
# above). **This file has drifted from the live host before (once at
# inception, then for ~2 months this time) — when in doubt, verify live**
# (`nomad node status -verbose <node-id>` or SSH in and read
# `/etc/nomad.d/server.hcl` directly) rather than trusting this copy
# blindly.

datacenter = "seedling-prod"
data_dir   = "/opt/nomad/data"
log_level  = "INFO"
name       = "aither"

limits {
  http_max_conns_per_client = 0
}

# External Nomad task-driver plugins (containerd, podman, singularity)
# live here. Drop new plugin binaries into this directory and restart
# Nomad to pick them up.
plugin_dir = "/opt/nomad/plugins"

# Bind to Tailscale interface only.
bind_addr = "{{ GetInterfaceIP \"tailscale0\" }}"
advertise {
  http = "{{ GetInterfaceIP \"tailscale0\" }}"
  rpc  = "{{ GetInterfaceIP \"tailscale0\" }}"
  serf = "{{ GetInterfaceIP \"tailscale0\" }}"
}

server {
  enabled          = true
  bootstrap_expect = 1

  # GC thresholds — effectively disable auto-GC of completed/dead
  # batch jobs, evals, and allocs so pipeline-head job records stay
  # queryable for post-hoc diagnostics (e.g. `abc job show` + log
  # retrieval after a pipeline failure days later). Nomad defaults
  # GC batch jobs at 24h and one-shot allocs at 1h, which made the
  # CholeraSeq eduan head disappear before its terminal log could
  # be retrieved (2026-05-26). Operator can still `nomad job stop
  # -purge` to remove a record explicitly; what we kill here is the
  # background sweep.
  #
  # 8760h = 1 year. Storage cost is small: a completed batch job's
  # raft entry is a few KB of job spec + eval metadata; alloc logs
  # live on the client filesystem under data_dir/alloc/<id>/ and
  # are sized by the job's stdout/stderr (capped by `logs { max_files
  # max_file_size }` in the task spec, typically a few MB per task).
  job_gc_threshold        = "8760h"
  batch_eval_gc_threshold = "8760h"
  eval_gc_threshold       = "8760h"
  deployment_gc_threshold = "8760h"
}

client {
  enabled           = true
  servers           = ["127.0.0.1:4647"]
  network_interface = "tailscale0"
  node_pool         = "platform"
  node_class        = "platform"

  # Client-side alloc GC — paired with server.hcl's job_gc_threshold
  # bump (2026-05-26). The server retains the job/eval records; the
  # client owns the alloc working directories under data_dir/alloc/
  # <id>/ where task stdout/stderr live. Default gc_max_allocs=50
  # means once a node has >50 terminal allocs it starts evicting
  # oldest — which made worker logs vanish on busy regression days.
  # Bumped to a number large enough that a year of typical pipeline
  # activity stays on disk. Disk-pressure GC (75% / 90% thresholds)
  # still triggers on actual disk fill, so this isn't a foot-gun on
  # constrained nodes.
  gc_max_allocs              = 5000
  gc_disk_usage_threshold    = 85
  gc_inode_usage_threshold   = 85

  meta {
    "node.locality.site"          = "stellenbosch"
    "node.locality.network"       = "tailscale"
    "node.locality.datacenter"    = "seedling-prod"
    "node.hardware.cpus"          = "48"
    "node.hardware.mem_gb"        = "251"
    "node.hardware.gpus"          = "0"
    "node.hardware.infiniband"    = "false"
    "node.os.name"                = "ubuntu"
    "node.os.version"             = "22.04"
    "node.software.apptainer"     = "1.5.0"
    "node.software.cuda"          = ""
    "node.software.s5cmd"         = "2.3.0"
    "node.capability.exec2"       = "true"
    "node.capability.podman"      = "true"
    "node.capability.singularity" = "true"
    "node.capability.docker"      = "true"
    "node.capability.containerd"  = "true"
    "node.compliance.popia"       = "true"
    "node.compliance.gdpr"        = "false"
    "node.compliance.kenya_dpa"   = "false"
    "node.workload.groups"        = "platform"
  }

  host_volume "minio-data" {
    path      = "/opt/abc-seedling/minio"
    read_only = false
  }
  host_volume "tusd-data" {
    path      = "/opt/abc-seedling/tusd"
    read_only = false
  }
  host_volume "nf-work" {
    path      = "/opt/abc-seedling/nf-work"
    read_only = false
  }
  # Worker-task scratch space — present on compute nodes (nomad01/02/03)
  # but missing here (aither is the platform node pool, added later);
  # provisioned 2026-07-04 to unblock worker tasks scheduled on aither.
  # Directory already existed at /opt/nomad/scratch (holding unrelated
  # mineru-cache/mineru-extraction content) — only the host_volume
  # registration was missing.
  host_volume "scratch" {
    path      = "/opt/nomad/scratch"
    read_only = false
  }
  # ADR-0061 shared tools volume. Deliberately read-only here (unlike
  # nomad01/02/03, which are read-write) — a prior RW mount request
  # failed placement on aither (see the 2026-06-26 tool-distribution
  # brainstorm). This asymmetry is load-bearing for the current
  # pipeline-head mount fix (PR #30 / commit e285678) and should not be
  # flipped without checking blast radius — see
  # brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md
  # for the full analysis.
  host_volume "abc-tools" {
    path      = "/opt/abc-cluster/abc-tools"
    read_only = true
  }
  host_volume "signup-svc" {
    path      = "/opt/abc-seedling/signup-svc"
    read_only = false
  }
  # Phase-B (ADR-0003 impl): durable obs storage for VM/VL/Grafana.
  host_volume "obs-data" {
    path      = "/opt/abc-seedling/obs"
    read_only = false
  }
  # Workbench homedir volume for user abhi
  host_volume "homedir-abhi" {
    path      = "/data/home/abhi/.abc"
    read_only = false
  }
}

acl {
  enabled = true
}

# Phase-B (ADR-0003 impl): Prometheus telemetry feeds VictoriaMetrics
# scrape via the obs-scrape ACL token.
telemetry {
  prometheus_metrics         = true
  publish_allocation_metrics = true
  publish_node_metrics       = true
  disable_hostname           = true
  collection_interval        = "10s"
}

plugin "docker" {
  config {
    # Image pull timeouts — bumped above Nomad's defaults so chonky
    # bioinformatics containers (e.g. MAGMA's magma-container-1 is
    # 2.6 GiB across 13 layers) can pull without restart-looping on
    # aither's link. Added 2026-06-03.
    #   image_pull_timeout      — total pull window (default 5m)
    #   pull_activity_timeout   — per-progress idle window (default 2m)
    image_pull_timeout    = "15m"
    pull_activity_timeout = "5m"
    volumes {
      enabled = true
    }
  }
}
plugin "raw_exec" {
  config { enabled = true }
}

# nomad-driver-exec2 (HashiCorp v0.1.2) — next-generation exec driver
# with bind mounts, capability-set control, and Linux landlock support.
# Allowed for all principals (sandboxed via cgroups + landlock + caps).
plugin "nomad-driver-exec2" {
  config {
    unveil_defaults = true
    unveil_paths    = []
    unveil_by_task  = false
  }
}

# qemu driver — Nomad built-in; no plugin stanza needed. Detection
# requires qemu-system-x86_64 on the host (apt install qemu-system-x86,
# done 2026-05-22). NOTE: aither has no nested virtualisation, so qemu
# falls back to TCG software emulation — usable for VM-image smoke
# tests, NOT for performance-sensitive workloads. The driver is in the
# CLI's restricted set so only privileged principals can submit qemu
# jobs anyway.

# External plugins (2026-05-22). Plugin name == binary name in plugin_dir.
plugin "containerd-driver" {
  config {
    enabled            = true
    containerd_runtime = "io.containerd.runc.v2"
    stats_interval     = "5s"
  }
}
plugin "nomad-driver-podman" {
  config {
    # Rootful podman system socket. Enable on aither with:
    #   sudo systemctl enable --now podman.socket
    socket_path = "unix:///run/podman/podman.sock"
  }
}

# nomad-driver-singularity — our fork at
# github.com/abc-cluster/nomad-driver-singularity (modernised from the
# upstream hpcng v1.0.0-alpha2 with Apptainer support, bug fixes, and
# updated dependencies; binary built from
# PHD-pub-abc-cluster/analysis/packages/nomad-driver-singularity).
# Resolves the 2017-msgpack-panic that crashed the unmaintained upstream
# alpha2 under modern Nomad. The old alpha2 binary is preserved on aither
# as /opt/nomad/plugins/.disabled-alpha2-nomad-driver-singularity for
# A/B testing if needed.
# NB: the stanza name must match the **plugin binary's filename** in
# plugin_dir, not the driver's internal registered name. The binary is
# /opt/nomad/plugins/nomad-driver-singularity so the stanza is
# "nomad-driver-singularity". (At runtime the driver registers itself as
# `singularity` — that's the name jobs use in `driver = "singularity"`.)
plugin "nomad-driver-singularity" {
  config {
    enabled = true
    # Explicit Apptainer binary path (singularity is not installed on
    # aither; the driver's $PATH search would fall back to apptainer
    # but this makes the intent explicit).
    singularity_path = "/usr/bin/apptainer"
  }
}
