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
        to = 3000
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
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-grafana"
        port     = "http"
        provider = "nomad"
        tags     = ["abc-nodes", "grafana", "ui"]
      }
    }
  }
}
