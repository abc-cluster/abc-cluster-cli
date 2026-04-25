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
  namespace = "abc-services"
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
        static = 9090
        to     = 9090
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
          "--web.enable-remote-write-receiver",
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
  - job_name: nomad
    metrics_path: /v1/metrics
    params:
      format: ["prometheus"]
    static_configs:
      - targets: ["100.70.185.46:4646"]
  - job_name: minio
    metrics_path: /minio/v2/metrics/cluster
    static_configs:
      - targets: ["100.70.185.46:9000"]
  - job_name: minio_bucket
    metrics_path: /minio/v2/metrics/bucket
    static_configs:
      - targets: ["100.70.185.46:9000"]
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
        provider = "consul"
        tags = [
          "abc-nodes", "prometheus", "metrics",
          "traefik.enable=true",
          "traefik.http.routers.prometheus.rule=Host(`prometheus.aither`)",
          "traefik.http.routers.prometheus.entrypoints=web",
          "traefik.http.services.prometheus.loadbalancer.server.port=9090",
        ]

        check {
          name     = "prometheus-health"
          type     = "http"
          path     = "/-/healthy"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
