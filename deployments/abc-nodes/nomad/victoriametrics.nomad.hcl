# VictoriaMetrics (metrics storage) — abc-nodes floor
#
# DROP-IN REPLACEMENT FOR PROMETHEUS
# ────────────────────────────────────
#  Same job ID (abc-nodes-prometheus), same Consul service name, same Traefik
#  vhost (prometheus.aither) — zero changes required in Grafana, Alloy, or
#  downstream tooling.  The scrape config is identical Prometheus YAML served
#  via -promscrape.config; VM forks Prometheus's service-discovery code so
#  consul_sd_configs and all relabel_configs work unchanged.
#
#  Key improvements over the prior Prometheus job:
#    • Persistent storage on aither's scratch host volume (Prometheus had none
#      — every restart lost all data).  90-day retention default.
#    • 5–10× lower memory for equivalent workload: ~256 MB vs 1536 MB.
#    • Remote-write receiver always on at /api/v1/write — no flag required.
#    • Out-of-order sample ingestion (Alloy reconnect after network partition).
#
# AUTO-DISCOVERY MODEL (unchanged from Prometheus)
# ─────────────────────────────────────────────────
#  consul_sd_configs drives all target discovery.  See promscrape.yml below.

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "vm_image" {
  type    = string
  default = "victoriametrics/victoria-metrics:v1.115.0"
}

variable "consul_address" {
  type        = string
  default     = "100.70.185.46:8500"
  description = "Consul agent (host:port) for consul_sd service discovery."
}

variable "retention_period" {
  type        = string
  default     = "90d"
  description = "How long to keep metrics on disk. VM format: 1d, 2w, 3m, 90d."
}

job "abc-nodes-prometheus" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "victoriametrics"
  }

  group "victoriametrics" {
    count = 1

    # Pin to aither: VM data lives on aither's scratch host volume.
    # Both Loki and MinIO are also pinned here — all storage services
    # co-locate so the scratch volume isn't split across nodes.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 8428
        to     = 8428
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # Pre-create the data directory before VM starts.
    task "ensure-data-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args       = ["mkdir -p /scratch/victoriametrics && chmod 0777 /scratch/victoriametrics"]
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

    task "victoriametrics" {
      driver = "containerd-driver"

      config {
        image = var.vm_image
        args = [
          "-storageDataPath=/scratch/victoriametrics",
          "-retentionPeriod=${var.retention_period}",
          "-promscrape.config=/local/promscrape.yml",
          "-httpListenAddr=:8428",
          # Suppress noise from Consul registering the same Nomad agent on
          # multiple ports (http/rpc/serf) — we keep only the http one via
          # relabeling, but VM still sees the duplicate targets during discovery.
          "-promscrape.suppressDuplicateScrapeTargetErrors=true",
        ]
      }

      template {
        # SIGHUP triggers a live reload of promscrape.yml without restart.
        change_mode   = "signal"
        change_signal = "SIGHUP"
        data          = <<EOF
global:
  scrape_interval: 15s
  external_labels:
    cluster: abc-nodes

scrape_configs:
  # ── Self ──────────────────────────────────────────────────────────────────
  - job_name: victoriametrics
    static_configs:
      - targets: ["127.0.0.1:8428"]

  # ── Nomad agents (servers + clients) ──────────────────────────────────────
  # Auto-discovered via Consul. Each agent registers MULTIPLE service entries —
  # one per port (http=4646, rpc=4647, serf=4648). We only want the http one.
  - job_name: nomad
    metrics_path: /v1/metrics
    params:
      format: ["prometheus"]
    consul_sd_configs:
      - server: "${var.consul_address}"
        services: ["nomad", "nomad-client"]
    relabel_configs:
      - source_labels: [__meta_consul_tags]
        regex: ".*,http,.*"
        action: keep
      - source_labels: [__meta_consul_service]
        target_label: nomad_role
      - source_labels: [__meta_consul_node]
        target_label: instance
      - source_labels: [__meta_consul_dc]
        target_label: dc

  # ── Consul agents ─────────────────────────────────────────────────────────
  - job_name: consul
    metrics_path: /v1/agent/metrics
    params:
      format: ["prometheus"]
    consul_sd_configs:
      - server: "${var.consul_address}"
        services: ["consul"]
    relabel_configs:
      - source_labels: [__meta_consul_address]
        target_label: __address__
        replacement: "$1:8500"
      - source_labels: [__meta_consul_node]
        target_label: instance
      - source_labels: [__meta_consul_dc]
        target_label: dc

  # ── Tag-driven generic scrape ─────────────────────────────────────────────
  # Any Consul-registered service tagged prometheus.scrape=true is picked up.
  # Optional tags: prometheus.path=/X, prometheus.scheme=https.
  - job_name: services
    consul_sd_configs:
      - server: "${var.consul_address}"
    relabel_configs:
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.scrape=true,.*"
        action: keep
      - source_labels: [__meta_consul_address, __meta_consul_service_port]
        separator: ":"
        target_label: __address__
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.path=([^,]+),.*"
        target_label: __metrics_path__
        replacement: "$1"
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.scheme=([^,]+),.*"
        target_label: __scheme__
        replacement: "$1"
      - source_labels: [__meta_consul_service]
        target_label: service
      - source_labels: [__meta_consul_node]
        target_label: instance
      - source_labels: [__meta_consul_dc]
        target_label: dc
EOF
        destination = "local/promscrape.yml"
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      resources {
        cpu    = 500
        memory = 512
      }

      # Keep the Consul service name as abc-nodes-prometheus so Grafana's
      # consul-template datasource block (range service "abc-nodes-prometheus")
      # picks up the new endpoint without any template changes.  The port
      # changes from 9090 to 8428; Grafana re-renders via change_mode=restart
      # on the datasource template when the registered port changes.
      service {
        name     = "abc-nodes-prometheus"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "victoriametrics", "metrics",
          "prometheus.scrape=true",
          "traefik.enable=true",
          # Keep prometheus.aither vhost — all existing bookmarks and Grafana
          # external datasource URLs continue to work.
          "traefik.http.routers.prometheus.rule=Host(`prometheus.aither`) || Host(`victoriametrics.aither`)",
          "traefik.http.routers.prometheus.entrypoints=web",
          "traefik.http.services.prometheus.loadbalancer.server.port=8428",
        ]

        check {
          name     = "victoriametrics-health"
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
