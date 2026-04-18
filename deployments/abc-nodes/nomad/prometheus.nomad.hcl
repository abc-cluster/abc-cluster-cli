# Prometheus (metrics) — abc-nodes floor
# Default: TSDB under /prometheus in the container (ephemeral).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "prometheus_image" {
  type    = string
  default = "prom/prometheus:v2.54.1"
}

job "abc-nodes-prometheus" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "prometheus"
  }

  group "prometheus" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        to = 9090
      }
    }

    task "prometheus" {
      driver = "containerd-driver"

      config {
        image = var.prometheus_image
        args = [
          "--config.file=/local/prometheus.yml",
          "--storage.tsdb.path=/prometheus",
          "--web.enable-lifecycle",
        ]
      }

      template {
        data = <<EOF
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["127.0.0.1:9090"]
EOF
        destination = "local/prometheus.yml"
      }

      resources {
        cpu    = 500
        memory = 1536
      }

      service {
        name     = "abc-nodes-prometheus"
        port     = "http"
        provider = "nomad"
        tags     = ["abc-nodes", "prometheus", "metrics"]
      }
    }
  }
}
