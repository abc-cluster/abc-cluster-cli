# VictoriaLogs (log storage) — abc-nodes floor
#
# DROP-IN REPLACEMENT FOR LOKI
# ─────────────────────────────
#  Same job ID (abc-nodes-loki), same Consul service name — Alloy's loki.write
#  and Grafana's datasource consul-template both resolve to the new service
#  automatically.
#
#  Key improvements over Loki:
#    • No S3 backend required — VictoriaLogs stores data locally in its own
#      compressed columnar format.  The loki MinIO bucket is now unused.
#    • Dramatically lower memory: typically 10× less than Loki for the same
#      workload.  Loki used 1536 MB; VictoriaLogs runs comfortably at 256 MB.
#    • LogsQL: richer query language, available via the native Grafana plugin
#      (victoriametrics-logs-datasource).  Basic LogQL label selectors like
#      {task=~"foo"} work unchanged.
#    • Persistent storage on the scratch host volume — same pattern as MinIO
#      and VictoriaMetrics.
#
#  Ingestion endpoint (Alloy → VictoriaLogs):
#    POST /insert/loki/api/v1/push   (Loki-compatible write path)
#
#  NOTE: VictoriaLogs does NOT expose a Loki-compatible query API.  Grafana
#  must use the victoriametrics-logs-datasource plugin for log queries.
#
#  VERSION NOTE
#  ────────────
#  Plugin v0.26.3 calls /select/logsql/field_values without the required
#  'field' parameter (plugin bug).  VictoriaLogs has required this param since
#  v1.x.  The 400 only affects query editor autocomplete; actual log panel
#  queries (LogsQL via /select/logsql/query) work fine.  This is a known
#  upstream plugin issue — upgrade the plugin when a fix is released.

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "vl_image" {
  type    = string
  default = "victoriametrics/victoria-logs:v1.50.0"
}

variable "retention_period" {
  type        = string
  default     = "90d"
  description = "How long to keep logs on disk. VL format: 1d, 2w, 3m, 90d."
}

job "abc-nodes-loki" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "victorialogs"
  }

  group "victorialogs" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 9428
        to     = 9428
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    task "ensure-data-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args       = ["mkdir -p /scratch/victorialogs && chmod 0777 /scratch/victorialogs"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "victorialogs" {
      driver = "containerd-driver"

      config {
        image = var.vl_image
        args = [
          "-storageDataPath=/scratch/victorialogs",
          "-retentionPeriod=${var.retention_period}",
          "-httpListenAddr=:9428",
        ]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-loki"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "victorialogs", "logs",
          "prometheus.scrape=true",
          "traefik.enable=true",
          # Keep loki.aither vhost for URL compatibility.
          "traefik.http.routers.loki.rule=Host(`loki.aither`) || Host(`victorialogs.aither`)",
          "traefik.http.routers.loki.entrypoints=web",
          "traefik.http.services.loki.loadbalancer.server.port=9428",
        ]

        check {
          name     = "victorialogs-health"
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
