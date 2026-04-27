# tusd (resumable uploads) backed by S3-compatible storage — abc-nodes floor
#
# BACKEND
# ───────
#  Default backend is RustFS (abc-nodes-rustfs) on port 9900. RustFS is preferred
#  over MinIO for the dual-network (LAN + Tailscale) deploy because MinIO Console
#  has hard-coded base-path issues across surfaces. The S3 API itself is
#  S3-compatible across both backends; switching is just an endpoint + credential
#  change.
#
#  To switch back to MinIO temporarily, override at deploy time:
#    -var='s3_endpoint=http://100.70.185.46:9000' \
#    -var='s3_access_key=minioadmin' -var='s3_secret_key=minioadmin'
#
# ENDPOINT FORMAT
# ───────────────
#  Endpoint must be the S3 *API* base URL — no path, no trailing slash. The AWS
#  SDK appends bucket/object segments. If uploads fail with the node's Tailscale
#  IP, try the LAN IP or another route that avoids hairpin/NAT.
#
# BUCKET BOOTSTRAP
# ────────────────
#  The prestart task creates the configured S3 bucket idempotently using awscli.
#  No manual setup required on first deploy.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "tusd_image" {
  type    = string
  default = "tusproject/tusd:v2.4.0"
}

variable "s3_endpoint" {
  type        = string
  description = "S3 API base URL (no path, no trailing slash) reachable from this allocation. Default = RustFS S3 port on aither."
  default     = "http://100.70.185.46:9900"
}

variable "s3_disable_content_hashes" {
  type        = bool
  description = "Pass -s3-disable-content-hashes (recommended for MinIO/RustFS to avoid multipart/hash quirks)"
  default     = true
}

locals {
  # tusd forwards this to AWS SDK BaseEndpoint; a trailing slash can produce bad requests.
  s3_endpoint_clean = trimsuffix(trimspace(var.s3_endpoint), "/")
}

variable "s3_bucket" {
  type    = string
  default = "tusd"
}

variable "s3_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "s3_secret_key" {
  type    = string
  default = "rustfsadmin"
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

    # ── Bucket bootstrap ───────────────────────────────────────────────────
    # Idempotently create the tusd bucket on the configured S3 backend before
    # tusd starts.  Uses awscli (`s3api create-bucket` is idempotent — already-exists
    # is treated as success).  Works against any S3-compatible endpoint (RustFS, MinIO).
    task "ensure-bucket" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "amazon/aws-cli:2.15.30"
        entrypoint = ["/bin/sh", "-c"]
        args = [<<-CMD
          set -e
          BUCKET="${var.s3_bucket}"
          ENDPOINT="${local.s3_endpoint_clean}"
          echo "[ensure-bucket] checking $BUCKET on $ENDPOINT"
          if aws --endpoint-url "$ENDPOINT" s3api head-bucket --bucket "$BUCKET" 2>/dev/null; then
            echo "[ensure-bucket] $BUCKET already exists"
            exit 0
          fi
          echo "[ensure-bucket] creating $BUCKET"
          aws --endpoint-url "$ENDPOINT" s3api create-bucket --bucket "$BUCKET" \
            || aws --endpoint-url "$ENDPOINT" s3 mb "s3://$BUCKET"
          echo "[ensure-bucket] done"
        CMD
        ]
      }

      template {
        destination = "secrets/aws.env"
        env         = true
        data        = <<EOF
AWS_ACCESS_KEY_ID=${var.s3_access_key}
AWS_SECRET_ACCESS_KEY=${var.s3_secret_key}
AWS_DEFAULT_REGION=${var.s3_region}
AWS_REGION=${var.s3_region}
EOF
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }

    task "tusd" {
      driver = "containerd-driver"

      config {
        image = var.tusd_image
        args = concat(
          [
            "-s3-bucket", var.s3_bucket,
            "-s3-endpoint", local.s3_endpoint_clean,
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
