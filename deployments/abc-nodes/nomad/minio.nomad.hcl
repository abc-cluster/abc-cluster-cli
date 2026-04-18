# MinIO object storage (service) — abc-nodes floor
# Default: data in the container filesystem (lost on reschedule). For production,
# replace with a group `volume` stanza + `volume_mount` backed by host_volume or CSI.

variable "datacenters" {
  type = list(string)
  # Include both common lab names so jobs schedule on single-node `default` DC clusters and typical `dc1` labs.
  default = ["dc1", "default"]
}

variable "minio_image" {
  type    = string
  default = "minio/minio:RELEASE.2024-12-18T13-15-44Z"
}

variable "minio_root_user" {
  type    = string
  default = "minioadmin"
}

variable "minio_root_password" {
  type    = string
  default = "minioadmin"
}

job "abc-nodes-minio" {
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
        to = 9000
      }
      port "console" {
        to = 9001
      }
    }

    task "minio" {
      driver = "containerd-driver"

      config {
        image = var.minio_image
        args = [
          "server", "/data",
          "--console-address", ":9001",
        ]
      }

      env {
        MINIO_ROOT_USER     = var.minio_root_user
        MINIO_ROOT_PASSWORD = var.minio_root_password
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-minio-s3"
        port     = "api"
        provider = "nomad"
        tags     = ["abc-nodes", "minio", "s3"]
      }

      service {
        name     = "abc-nodes-minio-console"
        port     = "console"
        provider = "nomad"
        tags     = ["abc-nodes", "minio", "console"]
      }
    }
  }
}
