# Prometheus — abc-nodes enhanced pack

job "abc-nodes-prometheus" {
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
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
        image = [[ var "prometheus_image" . | quote ]]
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
        tags = [
          "abc-nodes", "prometheus", "metrics",
          "traefik.enable=true",
          "traefik.http.routers.prometheus.rule=Host(`prometheus.aither`)",
          "traefik.http.services.prometheus.loadbalancer.server.port=9090",
        ]
      }
    }
  }
}
