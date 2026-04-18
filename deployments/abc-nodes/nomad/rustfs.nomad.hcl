# RustFS S3-compatible storage (service) — abc-nodes floor
# Default: container-local /data (see minio.nomad.hcl header for persistence options).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "rustfs_image" {
  type    = string
  default = "rustfs/rustfs:latest"
}

job "abc-nodes-rustfs" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "rustfs"
  }

  group "rustfs" {
    count = 1

    network {
      mode = "bridge"
      port "s3" {
        to = 9000
      }
    }

    task "rustfs" {
      driver = "containerd-driver"

      config {
        image = var.rustfs_image
        # Data path inside container; align with RustFS Docker docs for your tag.
        args = ["/data"]
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-rustfs-s3"
        port     = "s3"
        provider = "nomad"
        tags     = ["abc-nodes", "rustfs", "s3"]
      }
    }
  }
}
