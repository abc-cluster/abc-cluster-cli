# MinIO object storage (service) — abc-nodes floor
#
# DATA PERSISTENCE
# ─────────────────
#  Data stored at /opt/nomad/scratch/minio-data (via "scratch" host volume).
#  Safe across job restarts and Nomad upgrades — data is on the host FS.
#
# CREDENTIALS (Nomad Variables, namespace: services)
# ───────────────────────────────────────────────────
#  Path: nomad/jobs/abc-nodes-minio
#  Keys: minio_root_user, minio_root_password
#
#  Store / rotate:
#    abc admin services nomad cli -- var put -namespace services -force \
#      nomad/jobs/abc-nodes-minio \
#      minio_root_user=<user> minio_root_password=<password>
#
#  If the Variable is not set, falls back to the HCL variable defaults
#  (minioadmin/minioadmin) — change those defaults before first deploy.
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
  description = "Fallback only — override via Nomad Variable nomad/jobs/abc-nodes-minio"
}

variable "minio_root_password" {
  type        = string
  default     = "minioadmin"
  description = "Fallback only — override via Nomad Variable nomad/jobs/abc-nodes-minio"
}

job "abc-nodes-minio" {
  namespace   = "services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "minio"
  }

  group "minio" {
    count = 1

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

      # Credentials: Nomad Variable takes precedence over HCL variable defaults.
      # After HCL processing, ${var.minio_root_*} become the HCL default values.
      # At runtime the template engine uses the Variable if it exists, else falls back.
      template {
        destination = "secrets/minio.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-minio" -}}
MINIO_ROOT_USER={{ .minio_root_user }}
MINIO_ROOT_PASSWORD={{ .minio_root_password }}
{{- else -}}
MINIO_ROOT_USER=${var.minio_root_user}
MINIO_ROOT_PASSWORD=${var.minio_root_password}
{{- end }}
EOF
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-minio-s3"
        port     = "api"
        provider = "nomad"
        tags = [
          "abc-nodes", "minio", "s3",
          "traefik.enable=true",
          "traefik.http.routers.minio-s3.rule=Host(`minio.aither`)",
          "traefik.http.routers.minio-s3.service=minio-s3",
          "traefik.http.services.minio-s3.loadbalancer.server.port=9000",
        ]
      }

      service {
        name     = "abc-nodes-minio-console"
        port     = "console"
        provider = "nomad"
        tags = [
          "abc-nodes", "minio", "console",
          "traefik.enable=true",
          "traefik.http.routers.minio-console.rule=Host(`minio-console.aither`)",
          "traefik.http.routers.minio-console.service=minio-console",
          "traefik.http.services.minio-console.loadbalancer.server.port=9001",
        ]
      }
    }
  }
}
