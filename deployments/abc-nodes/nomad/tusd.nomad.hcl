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
  description = "MinIO S3 API base URL (no path, no trailing slash), reachable from the tusd container network namespace."
  default     = "http://100.70.185.46:9000"
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

variable "hook_url" {
  type    = string
  default = "http://100.77.21.36:14002/hook"
  description = <<-DESC
    URL of the post-finish hook server (fx-tusd-hook job, port 14002 on nomad01).
    Uses hardcoded Tailscale IP (100.77.21.36) because tusd runs in bridge-mode
    containers where Consul DNS (.service.consul) may not resolve.
    Set to "" to disable hooks.
  DESC
}

job "abc-nodes-tusd" {
  namespace   = "abc-services"
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

    # Note: ensure-s3-bucket prestart task removed — bucket is pre-created at cluster bootstrap.
    # If the tusd bucket is missing, create it manually via:
    #   mc mb --ignore-existing minio/tusd  (using MC_HOST_minio=http://minioadmin:minioadmin@100.70.185.46:9000)

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
          # Hook: rename S3 object to original filename after upload completes.
          # Deploy fx-tusd-hook first, then pass -var="hook_url=http://fx-tusd-hook.service.consul:14002/hook"
          var.hook_url != "" ? [
            "-hooks-http", var.hook_url,
            "-hooks-enabled-events", "post-finish",
          ] : [],
        )
      }

      template {
        destination = "secrets/s3.env"
        env         = true
        data        = <<EOF
AWS_ACCESS_KEY_ID=${var.s3_access_key}
AWS_SECRET_ACCESS_KEY=${var.s3_secret_key}
AWS_REGION=${var.s3_region}
EOF
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-tusd"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "tusd", "uploads",
          "traefik.enable=true",
          # ForwardAuth is applied by Caddy before reaching Traefik — no middleware here.
          "traefik.http.routers.tusd.rule=Host(`tusd.aither`)",
          "traefik.http.routers.tusd.entrypoints=web",
          "traefik.http.services.tusd.loadbalancer.server.port=8080",
        ]

        check {
          name     = "tusd-health"
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
