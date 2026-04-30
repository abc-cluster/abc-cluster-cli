# JuiceFS CSI — abc-nodes  [DESIGN DRAFT — NOT YET DEPLOYABLE]
# ────────────────────────────────────────────────────────────────────────────
# FUSE-path alternative to the s5cmd poststop sync (output-sync.nomad.hcl).
# When FUSE is available on a node, user jobs mount a JuiceFS volume instead
# of writing to ${NOMAD_ALLOC_DIR}/output/.  Data reaches RustFS in real-time
# via the JuiceFS FUSE layer — no poststop task needed.
#
# STATUS: design draft.  Not wired into Terraform.  Prerequisites listed below
# must be satisfied before this file can be deployed.
#
# ┌─────────────────────────────────────────────────────────────────────────┐
# │  ARCHITECTURE                                                           │
# │                                                                         │
# │  ┌──────────────────────────────────────────────────────────────┐      │
# │  │  User Job (Nomad alloc)                                       │      │
# │  │    task "main"                                                │      │
# │  │      volume_mount { /output → juicefs-user-scratch }         │      │
# │  │      writes files to /output/ as a normal POSIX filesystem   │      │
# │  └──────────────┬───────────────────────────────────────────────┘      │
# │                 │ FUSE                                                  │
# │  ┌──────────────▼───────────────────────────────────────────────┐      │
# │  │  JuiceFS Node Plugin (system job, one per non-spot node)      │      │
# │  │    • Manages FUSE mounts on the host                          │      │
# │  │    • Proxies metadata ops → PostgreSQL                        │      │
# │  │    • Streams data chunks → RustFS (S3)                        │      │
# │  └──────────┬────────────────────────┬────────────────────────┘       │
# │             │ metadata               │ data                            │
# │  ┌──────────▼──────────┐  ┌──────────▼──────────────────────────┐     │
# │  │  PostgreSQL          │  │  RustFS (S3)                        │     │
# │  │  abc-experimental    │  │  bucket: juicefs-meta  (chunks)     │     │
# │  │  database: juicefs   │  │  bucket: job-outputs   (logical FS) │     │
# │  └─────────────────────┘  └─────────────────────────────────────┘     │
# │                                                                         │
# │  JuiceFS Controller Plugin (service job, 1 instance on aither)         │
# │    • Handles CSI volume lifecycle (create/delete/attach/detach)        │
# │    • Translates Nomad CSI RPCs → JuiceFS filesystem operations         │
# └─────────────────────────────────────────────────────────────────────────┘
#
# PREREQUISITES (must be done before deploying this file)
# ────────────────────────────────────────────────────────
#  1. FUSE on target nodes
#     Each node that will run the node plugin needs:
#       • /dev/fuse device present
#       • fuse kernel module loaded (modprobe fuse)
#       • user_allow_other in /etc/fuse.conf
#     Mark FUSE-capable nodes:
#       nomad node meta apply -node-id=<id> fuse_available=true
#     The node plugin job has a constraint on this meta key.
#
#  2. JuiceFS filesystem format (one-time)
#     Run from any node with access to both PostgreSQL and RustFS:
#       juicefs format \
#         --storage s3 \
#         --bucket http://100.70.185.46:9900/juicefs-chunks \
#         --access-key rustfsadmin \
#         --secret-key rustfsadmin \
#         "postgres://juicefs:juicefs@100.70.185.46:5432/juicefs?sslmode=disable" \
#         abc-nodes
#     This is idempotent — safe to re-run.
#
#  3. PostgreSQL database + user
#     CREATE USER juicefs WITH PASSWORD 'juicefs';
#     CREATE DATABASE juicefs OWNER juicefs;
#     (or via Terraform null_resource + psql)
#
#  4. RustFS bucket for JuiceFS data chunks
#     mc mb rustfs/juicefs-chunks
#     This is separate from job-outputs — JuiceFS stores raw chunks here,
#     not human-readable files.
#
#  5. Nomad Variables for credentials
#     nomad var put nomad/jobs/abc-nodes-juicefs \
#       access_key_id=rustfsadmin \
#       secret_access_key=rustfsadmin \
#       pg_dsn="postgres://juicefs:juicefs@100.70.185.46:5432/juicefs?sslmode=disable"
#
# DESIGN DECISIONS (carried forward from output-sync.nomad.hcl)
# ──────────────────────────────────────────────────────────────
#  • PostgreSQL for metadata (not Redis) — already deployed, no new service
#  • RustFS as data backend — if multipart or ETag issues surface, switch
#    the --bucket URL to MinIO without changing anything else
#  • One JuiceFS filesystem ("abc-nodes") shared across all user jobs
#    Subdirectory-per-namespace isolation (not separate filesystems) to
#    avoid PostgreSQL connection-per-filesystem overhead at scale
#  • CSI controller pinned to aither (same as VictoriaLogs, Grafana)
#  • CSI node plugin excludes gcp-spot nodes (same constraint as Alloy)
#
# OPEN QUESTIONS (to resolve before deploying)
# ─────────────────────────────────────────────
#  □ Does RustFS support JuiceFS's multipart upload pattern cleanly?
#    Test: juicefs bench after format — if it hangs on upload, fall back to MinIO
#  □ JuiceFS CSI driver Nomad compatibility: the official image is
#    Kubernetes-centric.  Nomad CSI spec is compatible but some controller
#    lifecycle calls assume a kube API.  Validate with:
#      nomad plugin status juicefs
#    after deploying the controller — if it stays in "unhealthy" the kube
#    dependency needs to be stripped from the controller binary.
#  □ FUSE on GCP nodes: do the current machine images have /dev/fuse?
#    Run: ls -la /dev/fuse on a GCP node.  If absent, cloud-init can add it.

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

# https://hub.docker.com/r/juicedata/juicefs-csi-driver/tags
variable "juicefs_csi_image" {
  type    = string
  default = "juicedata/juicefs-csi-driver:v0.25.3"
}

variable "juicefs_fs_name" {
  type        = string
  default     = "abc-nodes"
  description = "JuiceFS filesystem name (created by `juicefs format`)."
}

variable "s3_endpoint" {
  type    = string
  default = "http://100.70.185.46:9900"
}

variable "s3_chunk_bucket" {
  type        = string
  default     = "juicefs-chunks"
  description = "RustFS bucket where JuiceFS stores raw data chunks (not human-readable)."
}

# ─── CSI Controller plugin ────────────────────────────────────────────────────
# One instance, pinned to aither.  Handles volume lifecycle RPCs from Nomad.
#
# DESIGN DECISION: controller pinned to aither (not floating).
# The controller writes to PostgreSQL (metadata) and RustFS (data).  Both are
# on aither's LAN.  Pinning avoids cross-DC latency for the control plane.
# The node plugins (below) run everywhere and do the actual FUSE I/O.

job "abc-nodes-juicefs-csi-controller" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "juicefs-csi-controller"
  }

  group "controller" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    # DESIGN DECISION: host network, not bridge.
    # The CSI controller communicates via a Unix socket with the Nomad client.
    # Nomad expects the socket at the path specified in csi_plugin.mount_dir.
    # Bridge networking would require bind-mounting the socket path, adding
    # complexity.  Host network is simpler and correct for a CSI controller.
    network {
      mode = "host"
    }

    task "juicefs-csi-controller" {
      driver = "docker"

      # DESIGN DECISION: docker driver, not containerd-driver.
      # The JuiceFS CSI image requires mount propagation (Bidirectional) and
      # /dev/fuse access.  containerd-driver's privileged support is less
      # battle-tested here; docker --privileged is the documented path.
      config {
        image      = var.juicefs_csi_image
        privileged = true

        # CSI spec: the socket lives at mount_dir/csi.sock
        # Nomad mounts mount_dir into the container at the same path.
        args = [
          "--endpoint=unix:///csi/csi.sock",
          "--logtostderr",
          "--v=3",
          "--nodeid=${node.unique.id}",
          "--role=controller",
        ]

        volumes = [
          "/var/lib/kubelet/plugins/juicefs.csi.k8s.io:/var/lib/kubelet/plugins/juicefs.csi.k8s.io",
        ]
        # NOTE: the kubelet path is a legacy from the Kubernetes origin of the
        # CSI driver.  Nomad ignores it but the binary may write state there.
        # Mount it so writes don't fail silently on a read-only path.
      }

      template {
        destination = "secrets/juicefs.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-nodes-juicefs" -}}
ACCESS_KEY={{ .access_key_id }}
SECRET_KEY={{ .secret_access_key }}
METAURL={{ .pg_dsn }}
{{- end }}
EOF
      }

      csi_plugin {
        id        = "juicefs"
        type      = "controller"
        mount_dir = "/csi"
      }

      resources {
        cpu    = 256
        memory = 256
      }
    }
  }
}

# ─── CSI Node plugin ─────────────────────────────────────────────────────────
# One instance per eligible node (system job).  Performs the actual FUSE mount
# for each allocation that requests a JuiceFS volume.
#
# DESIGN DECISION: exclude gcp-spot nodes (same as Alloy).
# Spot preemptions unmount FUSE in-flight — any in-progress write is lost.
# The poststop s5cmd path (output-sync.nomad.hcl) handles spot nodes; JuiceFS
# is only activated on stable (on-prem + reserved cloud) nodes.
#
# DESIGN DECISION: node.meta.fuse_available constraint.
# Only nodes explicitly opted-in to FUSE carry this meta key.  This prevents
# the node plugin from being scheduled on nodes where /dev/fuse is absent,
# which would cause a silent "volume mount failed" error in user jobs.

job "abc-nodes-juicefs-csi-node" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "system"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "juicefs-csi-node"
  }

  group "node" {
    # Only nodes with FUSE available
    constraint {
      attribute = "${node.meta.fuse_available}"
      value     = "true"
    }

    # Exclude spot — preemption mid-write = data loss
    constraint {
      attribute = "${node.class}"
      operator  = "!="
      value     = "gcp-spot"
    }

    network {
      mode = "host"
    }

    task "juicefs-csi-node" {
      driver = "docker"

      config {
        image      = var.juicefs_csi_image
        privileged = true

        args = [
          "--endpoint=unix:///csi/csi.sock",
          "--logtostderr",
          "--v=3",
          "--nodeid=${node.unique.id}",
          "--role=node",
        ]

        # Required for FUSE: the mount must propagate from container → host
        # so that Nomad (running on the host) can bind-mount the filesystem
        # into allocation directories.
        mount {
          type   = "bind"
          source = "/dev/fuse"
          target = "/dev/fuse"
        }
        mount {
          type        = "bind"
          source      = "/var/lib/kubelet"
          target      = "/var/lib/kubelet"
          bind_options { propagation = "rshared" }
        }
      }

      template {
        destination = "secrets/juicefs.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-nodes-juicefs" -}}
ACCESS_KEY={{ .access_key_id }}
SECRET_KEY={{ .secret_access_key }}
METAURL={{ .pg_dsn }}
{{- end }}
EOF
      }

      csi_plugin {
        id        = "juicefs"
        type      = "node"
        mount_dir = "/csi"
      }

      resources {
        cpu    = 256
        memory = 512
        # DESIGN DECISION: 512 MB for node plugin.
        # JuiceFS node plugin buffers read/write ops in memory.  The default
        # cache-size (JuiceFS --cache-size flag) is 1 GB but we set it to 256 MB
        # to stay within the Nomad-visible limit.  The plugin uses memory outside
        # Nomad's accounting for the FUSE kernel buffer (not controllable here).
      }
    }
  }
}

# ─── Nomad CSI Volume registration (example) ─────────────────────────────────
# After both plugin jobs are healthy, register the volume with:
#
#   nomad volume create - <<EOF
#   id           = "juicefs-user-scratch"
#   name         = "juicefs-user-scratch"
#   type         = "csi"
#   plugin_id    = "juicefs"
#   capacity_min = "10 GiB"
#   capacity_max = "1 TiB"
#
#   capability {
#     access_mode     = "multi-node-multi-writer"
#     attachment_mode = "file-system"
#   }
#
#   parameters {
#     metaurl    = "postgres://juicefs:juicefs@100.70.185.46:5432/juicefs?sslmode=disable"
#     storage    = "s3"
#     bucket     = "http://100.70.185.46:9900/juicefs-chunks"
#     access-key = "rustfsadmin"
#     secret-key = "rustfsadmin"
#     name       = "abc-nodes"
#   }
#   EOF
#
# Then user jobs reference it with:
#
#   volume "workdir" {
#     type            = "csi"
#     source          = "juicefs-user-scratch"
#     attachment_mode = "file-system"
#     access_mode     = "multi-node-multi-writer"
#   }
#
#   task "main" {
#     volume_mount {
#       volume      = "workdir"
#       destination = "/output"
#       read_only   = false
#     }
#   }
