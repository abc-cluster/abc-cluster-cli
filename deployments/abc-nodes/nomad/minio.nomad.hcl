# MinIO object storage (service) — abc-nodes floor
#
# DATA PERSISTENCE
# ─────────────────
#  Data stored at /opt/nomad/scratch/minio-data (via "scratch" host volume).
#  Safe across job restarts and Nomad upgrades — data is on the host FS.
#
# CREDENTIALS STRATEGY
# ────────────────────
#  Bootstrap/default-first: this job starts using HCL defaults
#  (minioadmin/minioadmin) so first deployments do not depend on Nomad Variables.
#
#  Later hardening: migrate to Nomad Variables or Vault and update this job to
#  consume secret references once secure token workflows are in place.
#
# After rotating credentials:
#   1. Update the Nomad Variable (command above)
#   2. Redeploy: abc admin services nomad cli -- job run deployments/abc-nodes/nomad/minio.nomad.hcl
#   3. Update the mc alias: mc alias set sunminio http://100.70.185.46:9000 <user> <pass>

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "minio_image" {
  type    = string
  default = "minio/minio:RELEASE.2024-12-18T13-15-44Z"
}

variable "minio_root_user" {
  type        = string
  default     = "minioadmin"
  description = "Bootstrap default root user"
}

variable "minio_root_password" {
  type        = string
  default     = "minioadmin"
  description = "Bootstrap default root password"
}

job "abc-nodes-minio" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "minio"
  }

  group "minio" {
    count = 1

    # Pin to aither: MinIO data lives on aither's scratch host volume.
    # Nomad's built-in host-volume placement (volume "scratch") already prevents
    # scheduling on nodes that don't declare the volume, but this constraint
    # makes the intent explicit and guards against accidentally declaring the
    # volume on a new node.
    # Verify with: nomad node status -self  (check "Name" field on aither)
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "api" {
        static = 9000
        to     = 9000
      }
      port "console" {
        static = 9001
        to     = 9001
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    task "minio" {
      driver = "containerd-driver"

      config {
        image = var.minio_image
        args = [
          "server", "/scratch/minio-data",
          "--console-address", ":9001",
        ]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      # Bootstrap mode: always use HCL defaults.
      # Migrate to Nomad/Vault-backed secrets once secure token workflows are enabled.
      template {
        destination = "secrets/minio.env"
        env         = true
        data        = <<EOF
MINIO_ROOT_USER=${var.minio_root_user}
MINIO_ROOT_PASSWORD=${var.minio_root_password}
# Expose Prometheus metrics endpoint without auth for in-cluster scraping.
MINIO_PROMETHEUS_AUTH_TYPE=public
EOF
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-minio-s3"
        port     = "api"
        provider = "consul"
        tags = [
          "abc-nodes", "minio", "s3",
          "traefik.enable=true",
          "traefik.http.routers.minio-s3.rule=Host(`minio.aither`)",
          "traefik.http.routers.minio-s3.entrypoints=web",
          "traefik.http.services.minio-s3.loadbalancer.server.port=9000",
        ]

        check {
          name     = "minio-s3-health"
          type     = "http"
          path     = "/minio/health/live"
          interval = "15s"
          timeout  = "3s"
        }
      }

      service {
        name     = "abc-nodes-minio-console"
        port     = "console"
        provider = "consul"
        tags = [
          "abc-nodes", "minio", "console",
          "traefik.enable=true",
          "traefik.http.routers.minio-console.rule=Host(`minio-console.aither`)",
          "traefik.http.routers.minio-console.entrypoints=web",
          "traefik.http.services.minio-console.loadbalancer.server.port=9001",
        ]

        # TCP check: the console (port 9001) doesn't expose a dedicated health path.
        check {
          name     = "minio-console-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
