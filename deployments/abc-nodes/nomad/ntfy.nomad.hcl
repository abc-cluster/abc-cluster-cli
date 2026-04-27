# ntfy (push notifications) — abc-nodes floor
# Attachments stored in RustFS S3 bucket "ntfy".

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
  description = "Public URL for ntfy (must be host root, no path)."
  # ntfy must be served at a HOST ROOT (it cannot live under a subpath because
  # generated absolute URLs — attachment links, sharing links, web push registration —
  # need a clean origin). On the LAN surface ntfy is reachable at
  # http://aither.mb.sun.ac.za/ntfy/ via Caddy's Referer-based routing for the
  # web UI and API calls, but base-url must be the Tailscale-side hostname so
  # that those generated absolute URLs resolve correctly on the primary surface.
  default     = "http://ntfy.aither"
}

variable "s3_endpoint" {
  type        = string
  description = "RustFS S3 host:port without scheme, e.g. 100.70.185.46:9900"
  default     = "100.70.185.46:9900"
}

variable "s3_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "s3_secret_key" {
  type    = string
  default = "rustfsadmin"
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

    # ── Ensure ntfy bucket exists on RustFS ──────────────────────────────────
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
          BUCKET="${var.ntfy_attachment_bucket}"
          ENDPOINT="http://${var.s3_endpoint}"
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
AWS_DEFAULT_REGION=us-east-1
AWS_REGION=us-east-1
EOF
      }

      resources {
        cpu    = 100
        memory = 128
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
  endpoint: "http://${var.s3_endpoint}"
  bucket: "${var.ntfy_attachment_bucket}"
  access-key: "${var.s3_access_key}"
  secret-key: "${var.s3_secret_key}"
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
