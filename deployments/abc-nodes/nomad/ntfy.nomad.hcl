# ntfy (push notifications) — abc-nodes floor
# Attachments stored in MinIO S3 bucket "ntfy".

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "ntfy_image" {
  type    = string
  default = "binwiederhier/ntfy:v2.11.0"
}

variable "ntfy_base_url" {
  type        = string
  description = "Public URL for ntfy (must be host root, no path). Served at its own vhost ntfy.aither."
  default     = "http://ntfy.aither"
}

variable "minio_endpoint" {
  type        = string
  description = "MinIO host:port without scheme, e.g. 100.70.185.46:9000"
  default     = "100.70.185.46:9000"
}

variable "minio_access_key" {
  type    = string
  default = "minioadmin"
}

variable "minio_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "ntfy_attachment_bucket" {
  type    = string
  default = "ntfy"
}

job "abc-nodes-ntfy" {
  namespace = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "ntfy"
  }

  group "ntfy" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 8088
        to     = 80
      }
    }

    task "ntfy" {
      driver = "containerd-driver"

      config {
        image = var.ntfy_image
        args  = ["serve", "--config", "/local/ntfy.yml"]
      }

      template {
        data        = <<EOF
base-url: "${var.ntfy_base_url}"
behind-proxy: true
listen-http: ":80"

attachment-cache-dir: ""
attachment-expiry-duration: "3h"
attachment-total-size-limit: "5G"
attachment-file-size-limit: "15M"

attachment-s3:
  endpoint: "http://${var.minio_endpoint}"
  bucket: "${var.ntfy_attachment_bucket}"
  access-key: "${var.minio_access_key}"
  secret-key: "${var.minio_secret_key}"
  region: "us-east-1"
  path-style: true
EOF
        destination = "local/ntfy.yml"
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-ntfy"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "ntfy", "notifications",
          "traefik.enable=true",
          "traefik.http.routers.ntfy.rule=Host(`ntfy.aither`)",
          "traefik.http.routers.ntfy.entrypoints=web",
          "traefik.http.services.ntfy.loadbalancer.server.port=8088",
        ]

        check {
          name     = "ntfy-health"
          type     = "http"
          path     = "/v1/health"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
