# VictoriaTraces (distributed tracing) — abc-nodes floor
#
# INFRASTRUCTURE READINESS
# ─────────────────────────
#  VictoriaTraces v0.8.x is pre-release.  This job deploys the storage backend
#  so the cluster is ready to receive traces once workloads are instrumented.
#  No Grafana datasource is pre-provisioned — add it once you have trace data
#  and can validate the query experience.
#
# INGESTION
# ─────────
#  VictoriaTraces accepts OTLP only (no native Jaeger/Zipkin).
#  OTLP HTTP: POST http://host:10428/insert/opentelemetry/v1/traces
#  OTLP gRPC: host:4317
#
#  To instrument a workload, configure the OTLP exporter:
#    OTEL_EXPORTER_OTLP_ENDPOINT=http://100.70.185.46:10428
#    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://100.70.185.46:10428/insert/opentelemetry/v1/traces
#
# GRAFANA SETUP (manual — do after workloads are instrumented)
# ─────────────────────────────────────────────────────────────
#  VictoriaTraces exposes a query API at http://host:10428.  Add a datasource
#  once the native Grafana plugin is published (expected as product matures).
#  Until then, query via the VictoriaTraces UI at http://victoriatraces.aither.

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "vt_image" {
  type    = string
  default = "victoriametrics/victoria-traces:v0.8.2"
}

variable "retention_period" {
  type        = string
  default     = "14d"
  description = "Trace retention. Spans accumulate faster than metrics; 14d is a sensible default."
}

job "abc-nodes-victoriatraces" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "victoriatraces"
  }

  group "victoriatraces" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 10428
        to     = 10428
      }
      port "otlp_grpc" {
        static = 4317
        to     = 4317
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
        args       = ["mkdir -p /scratch/victoriatraces && chmod 0777 /scratch/victoriatraces"]
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

    task "victoriatraces" {
      driver = "containerd-driver"

      config {
        image = var.vt_image
        args = [
          "-storageDataPath=/scratch/victoriatraces",
          "-retentionPeriod=${var.retention_period}",
          "-httpListenAddr=:10428",
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
        name     = "abc-nodes-victoriatraces"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "victoriatraces", "tracing",
          "prometheus.scrape=true",
          "traefik.enable=true",
          "traefik.http.routers.victoriatraces.rule=Host(`victoriatraces.aither`)",
          "traefik.http.routers.victoriatraces.entrypoints=web",
          "traefik.http.services.victoriatraces.loadbalancer.server.port=10428",
        ]

        check {
          name     = "victoriatraces-health"
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
