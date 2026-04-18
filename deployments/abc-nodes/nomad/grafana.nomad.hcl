# Grafana (dashboards) — abc-nodes floor
# Default: Grafana data in the container (ephemeral).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "grafana_image" {
  type    = string
  default = "grafana/grafana:11.4.0"
}

variable "grafana_admin_password" {
  type    = string
  default = "admin"
}

job "abc-nodes-grafana" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "grafana"
  }

  group "grafana" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 3000
        to     = 3000
      }
    }

    task "grafana" {
      driver = "containerd-driver"

      config {
        image = var.grafana_image
      }

      env {
        GF_SECURITY_ADMIN_PASSWORD = var.grafana_admin_password
        GF_SERVER_HTTP_PORT        = "3000"
        GF_PATHS_PROVISIONING      = "/local/provisioning"
      }

      template {
        data        = <<EOF
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    url: http://100.70.185.46:9090
    access: proxy
    isDefault: true
    editable: false

  - name: Loki
    type: loki
    uid: loki
    url: http://100.70.185.46:3100
    access: proxy
    isDefault: false
    editable: false
EOF
        destination = "local/provisioning/datasources/default.yaml"
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-grafana"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "grafana", "ui",
          "traefik.enable=true",
          "traefik.http.routers.grafana.rule=Host(`grafana.aither`)",
          "traefik.http.services.grafana.loadbalancer.server.port=3000",
        ]
      }
    }
  }
}
