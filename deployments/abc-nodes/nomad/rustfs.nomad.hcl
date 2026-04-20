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

variable "rustfs_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "rustfs_secret_key" {
  type    = string
  default = "rustfsadmin"
}

job "abc-nodes-rustfs" {
  namespace = "services"
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
        static = 9900
        to     = 9000
      }
      port "console" {
        static = 9901
        to     = 9001
      }
    }

    task "rustfs" {
      driver = "containerd-driver"

      config {
        image = var.rustfs_image
        args  = ["/data"]
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
        provider = "nomad"
        tags = [
          "abc-nodes", "rustfs", "s3",
          "traefik.enable=true",
          "traefik.http.routers.rustfs.rule=Host(`rustfs.aither`)",
          "traefik.http.services.rustfs.loadbalancer.server.port=9900",
        ]
      }

      service {
        name     = "abc-nodes-rustfs-console"
        port     = "console"
        provider = "nomad"
        tags     = ["abc-nodes", "rustfs", "console"]
      }
    }
  }
}
