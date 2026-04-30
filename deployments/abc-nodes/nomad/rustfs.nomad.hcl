# RustFS S3-compatible storage (service) — abc-nodes floor
#
# DATA PERSISTENCE
# ────────────────
#  Data stored at /opt/nomad/scratch/rustfs-data on aither (via the "scratch"
#  host volume).  Survives job restarts and node reboots.
#
# ROLE IN THE CLUSTER
# ───────────────────
#  Primary S3 backend for tusd (uppy) uploads — RustFS works correctly across
#  both the LAN and Tailscale surfaces, whereas MinIO Console has dual-mode
#  base-path issues.  MinIO is still deployed for now; data-transfer automations
#  between the two will be added separately.
#
# ENDPOINTS
# ─────────
#  S3 API  : http://100.70.185.46:9900   (or http://rustfs.aither/)
#  Console : http://100.70.185.46:9901   (or http://rustfs-console.aither/)
#
# CREDENTIALS (bootstrap defaults, rotate via Vault later)
#  RUSTFS_ACCESS_KEY=rustfsadmin
#  RUSTFS_SECRET_KEY=rustfsadmin

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "rustfs_image" {
  type    = string
  default = "rustfs/rustfs:latest"
}

variable "rustfs_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "rustfs_secret_key" {
  type    = string
  default = "rustfsadmin"
}

job "abc-nodes-rustfs" {
  namespace = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "rustfs"
    restart_nonce    = "2026-04-29-tusd-multipart"
  }

  group "rustfs" {
    count = 1

    # Pin to aither: data lives on aither's scratch host volume.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "s3" {
        static = 9900
        to     = 9000
      }
      port "console" {
        static = 9901
        to     = 9001
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # ── Ensure data dir exists ────────────────────────────────────────────
    # The RustFS container runs as a non-root user and won't auto-create
    # the data directory if it's missing.  Pre-create with permissive
    # perms so RustFS can write regardless of its internal UID.
    task "ensure-data-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args       = ["mkdir -p /scratch/rustfs-data && chmod 0777 /scratch/rustfs-data"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "rustfs" {
      driver = "containerd-driver"

      config {
        image = var.rustfs_image
        args  = [
          "--access-key", var.rustfs_access_key,
          "--secret-key", var.rustfs_secret_key,
          "/scratch/rustfs-data",
        ]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        RUSTFS_ACCESS_KEY     = var.rustfs_access_key
        RUSTFS_SECRET_KEY     = var.rustfs_secret_key
        RUSTFS_CONSOLE_ENABLE = "true"
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-rustfs-s3"
        port     = "s3"
        provider = "consul"
        tags = [
          "abc-nodes", "rustfs", "s3",
          "traefik.enable=true",
          "traefik.http.routers.rustfs.rule=Host(`rustfs.aither`)",
          "traefik.http.routers.rustfs.entrypoints=web",
          "traefik.http.services.rustfs.loadbalancer.server.port=9900",
        ]

        # TCP check: RustFS doesn't expose /minio/health/live (MinIO-specific).
        check {
          name     = "rustfs-s3-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }

      service {
        name     = "abc-nodes-rustfs-console"
        port     = "console"
        provider = "consul"
        tags = [
          "abc-nodes", "rustfs", "console",
          "traefik.enable=true",
          "traefik.http.routers.rustfs-console.rule=Host(`rustfs-console.aither`)",
          "traefik.http.routers.rustfs-console.entrypoints=web",
          "traefik.http.services.rustfs-console.loadbalancer.server.port=9901",
        ]

        check {
          name     = "rustfs-console-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
