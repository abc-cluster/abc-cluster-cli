# ntfy (push notifications) — abc-nodes floor

job "abc-nodes-ntfy" {
  namespace   = [[ var "abc_services_namespace" . | quote ]]
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
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
        image = [[ var "ntfy_image" . | quote ]]
        args  = ["serve", "--config", "/local/ntfy.yml"]
      }

      template {
        data        = <<EOF
base-url: "[[ var "ntfy_base_url" . ]]"
behind-proxy: true
listen-http: ":80"

attachment-cache-dir: ""
attachment-expiry-duration: "3h"
attachment-total-size-limit: "5G"
attachment-file-size-limit: "15M"

attachment-s3:
  endpoint: "http://[[ var "ntfy_minio_endpoint" . ]]"
  bucket: "[[ var "ntfy_attachment_bucket" . ]]"
  access-key: "[[ var "ntfy_minio_access_key" . ]]"
  secret-key: "[[ var "ntfy_minio_secret_key" . ]]"
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
        provider = "nomad"
        tags = [
          "abc-nodes", "ntfy", "notifications",
          "traefik.enable=true",
          "traefik.http.routers.ntfy.rule=Host(`ntfy.aither`)",
          "traefik.http.services.ntfy.loadbalancer.server.port=8088",
        ]
      }
    }
  }
}
