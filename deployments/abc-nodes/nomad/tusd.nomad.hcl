# tusd (resumable uploads) backed by S3-compatible storage — abc-nodes floor
#
# Endpoint must be the MinIO *S3 API* base URL (task port "api" / in-container :9000),
# reachable from this allocation's network namespace — not the console port (:9001).
# Omit a trailing slash (e.g. use http://host:21400 not http://host:21400/) so the
# AWS SDK does not build malformed URLs. If uploads fail with the node's Tailscale
# IP, try the LAN IP or another route that avoids hairpin/NAT back to the same host.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "tusd_image" {
  type    = string
  default = "tusproject/tusd:v2.4.0"
}

variable "minio_s3_endpoint" {
  type        = string
  description = "MinIO S3 API base URL (no path, no trailing slash), e.g. http://192.168.1.10:21400"
  default     = "http://127.0.0.1:9000"
}

variable "s3_disable_content_hashes" {
  type        = bool
  description = "Pass -s3-disable-content-hashes (recommended for MinIO to avoid multipart/hash quirks)"
  default     = true
}

locals {
  # tusd forwards this to AWS SDK BaseEndpoint; a trailing slash can produce bad requests.
  minio_s3_endpoint_clean = trimsuffix(trimspace(var.minio_s3_endpoint), "/")
}

variable "s3_bucket" {
  type    = string
  default = "tusd"
}

variable "s3_access_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_region" {
  type    = string
  default = "us-east-1"
}

job "abc-nodes-tusd" {
  namespace = "services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "tusd"
  }

  group "tusd" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 8080
        to     = 8080
      }
    }

    task "tusd" {
      driver = "containerd-driver"

      config {
        image = var.tusd_image
        args = concat(
          [
            "-s3-bucket", var.s3_bucket,
            "-s3-endpoint", local.minio_s3_endpoint_clean,
            "-s3-disable-ssl",
            "-port", "8080",
            "-base-path", "/files/",
          ],
          var.s3_disable_content_hashes ? ["-s3-disable-content-hashes"] : [],
        )
      }

      env {
        AWS_ACCESS_KEY_ID     = var.s3_access_key
        AWS_SECRET_ACCESS_KEY = var.s3_secret_key
        AWS_REGION            = var.s3_region
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-tusd"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "tusd", "http",
          "traefik.enable=true",
          "traefik.http.routers.tusd.rule=Host(`tusd.aither`)",
          "traefik.http.services.tusd.loadbalancer.server.port=8080",
        ]
      }
    }
  }
}
