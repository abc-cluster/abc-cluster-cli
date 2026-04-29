# Prometheus (metrics) — abc-nodes floor
#
# AUTO-DISCOVERY MODEL
# ────────────────────
#  All scrape targets are discovered via Consul (consul_sd_configs).  Adding a
#  new node, datacenter, or service requires NO change to this file:
#
#    • Nomad agents      — auto-discovered as Consul services "nomad"
#                          (servers) and "nomad-client" (clients).
#    • Consul agents     — auto-discovered as the "consul" service.
#    • Everything else   — opt-in via Consul service tags:
#        prometheus.scrape=true        REQUIRED to be scraped at all
#        prometheus.path=/X            optional metrics path  (default /metrics)
#        prometheus.scheme=https       optional               (default http)
#
#  The convention mirrors Traefik's tag-driven approach: services declare
#  themselves discoverable in their jobspec's `service { tags = [...] }` block.
#  This is namespace-agnostic — discovery covers every namespace simultaneously.
#
#  Network mode = host so we can reach the local Consul agent at 127.0.0.1:8500
#  (every Nomad client runs a Consul agent).  Static port 9090 is fine because
#  count = 1; the host_volume constraint pins us to nodes that have the data dir.
#
# Default: TSDB under /prometheus in the container (ephemeral).

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "prometheus_image" {
  type    = string
  # v3.x ships the new Mantine-based browser UI by default.  See
  # https://prometheus.io/docs/visualization/browser/ — the new graph view,
  # explorer, and PromQL UI come for free with the version bump.
  default = "prom/prometheus:v3.5.0"
}

variable "external_url" {
  type        = string
  default     = "http://prometheus.aither"
  description = "External URL prometheus thinks it's served at; used to rewrite redirects + UI links so navigation works through the Traefik vhost."
}

variable "consul_address" {
  type        = string
  default     = "100.70.185.46:8500"
  description = "Consul agent (host:port) to use for Consul-side service discovery. Any Consul agent joined to the cluster works."
}


job "abc-nodes-prometheus" {
  namespace   = "abc-services"
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
      # Bridge mode (matches the rest of the containerd-driver fleet on this
      # cluster). The host's Consul agent is reached via the host's primary
      # IP rendered into the config at template time — see consul_sd_configs
      # `server:` lines below.
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
          # Tell the new Mantine browser UI which URL it lives at, so its
          # client-side router builds correct links when accessed through the
          # Traefik vhost (prometheus.aither) instead of the bare port.
          "--web.external-url=${var.external_url}",
        ]
      }

      template {
        data = <<EOF
global:
  scrape_interval: 15s
  external_labels:
    cluster: abc-nodes

scrape_configs:
  # ── Self ──────────────────────────────────────────────────────────────────
  - job_name: prometheus
    static_configs:
      - targets: ["127.0.0.1:9090"]

  # ── Nomad agents (servers + clients) ──────────────────────────────────────
  # Auto-discovered via Consul. Each agent registers MULTIPLE service entries —
  # one per port (http=4646, rpc=4647, serf=4648). We only want the http one.
  #
  # NOTE — multi-DC coverage requirement: services + Nomad agents are only
  # discoverable here if their host runs a Consul agent joined to the catalog
  # at ${var.consul_address}.  In a cluster where Consul is centralised on
  # one node (current state on aither: only aither shows up in Consul), the
  # heavy lifting for per-node observability is handled by the Alloy `system`
  # job — it ships every node's host metrics + local Nomad metrics to
  # Prometheus via remote_write, so Prometheus doesn't need to *pull* from
  # nodes that don't have Consul.  Add a Consul agent to a new node to also
  # let it appear in Prometheus's pull discovery.
  - job_name: nomad
    metrics_path: /v1/metrics
    params:
      format: ["prometheus"]
    consul_sd_configs:
      - server: "${var.consul_address}"
        services: ["nomad", "nomad-client"]
    relabel_configs:
      # Keep only the HTTP-tagged registrations (drop rpc + serf ports).
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
  # Consul registers itself with the RPC port (8300); we override to hit the
  # HTTP API port (8500) where /v1/agent/metrics lives.
  - job_name: consul
    metrics_path: /v1/agent/metrics
    params:
      format: ["prometheus"]
    consul_sd_configs:
      - server: "${var.consul_address}"
        services: ["consul"]
    relabel_configs:
      # Rewrite scrape target to <node_address>:8500 (the HTTP API).
      - source_labels: [__meta_consul_address]
        target_label: __address__
        replacement: "$1:8500"
      - source_labels: [__meta_consul_node]
        target_label: instance
      - source_labels: [__meta_consul_dc]
        target_label: dc

  # ── Tag-driven generic scrape ─────────────────────────────────────────────
  # Any Consul-registered service tagged `prometheus.scrape=true` is picked up.
  # Optional tags: prometheus.path=/X, prometheus.scheme=https.
  - job_name: services
    consul_sd_configs:
      - server: "${var.consul_address}"
    relabel_configs:
      # Keep only opt-in services.
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.scrape=true,.*"
        action: keep
      # Rewrite scrape target to use the node's address rather than the
      # service address. Nomad-bridge allocs register their bind IP (often
      # 127.0.0.1) as ServiceAddress, but the host port is reachable on
      # the node's primary IP — which is exactly __meta_consul_address.
      - source_labels: [__meta_consul_address, __meta_consul_service_port]
        separator: ":"
        target_label: __address__
      # metrics path override (tag: prometheus.path=/foo)
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.path=([^,]+),.*"
        target_label: __metrics_path__
        replacement: "$1"
      # scheme override (tag: prometheus.scheme=https)
      - source_labels: [__meta_consul_tags]
        regex: ".*,prometheus\\.scheme=([^,]+),.*"
        target_label: __scheme__
        replacement: "$1"
      # Useful labels.
      - source_labels: [__meta_consul_service]
        target_label: service
      - source_labels: [__meta_consul_node]
        target_label: instance
      - source_labels: [__meta_consul_dc]
        target_label: dc
EOF
        destination = "local/prometheus.yml"
        change_mode = "signal"
        change_signal = "SIGHUP"
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
          # Self-scrape via tag (covered by static_configs above too — harmless dupe).
          "prometheus.scrape=true",
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
