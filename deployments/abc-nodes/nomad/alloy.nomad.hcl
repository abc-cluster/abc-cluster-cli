# Grafana Alloy (observability agent) — abc-nodes floor
# Collects: host metrics (unix exporter), Nomad per-node metrics, Nomad alloc
# logs from the local host filesystem. Ships metrics → Prometheus, logs → Loki.
#
# type=system: one allocation per eligible node so every Nomad client tails its
# own alloc logs (count=1 would only ship logs from workloads on that single node).
#
# raw_exec + host network: read /var/lib/nomad/... and reach the central Loki /
# Prometheus HTTP ports on the cluster host (host part of nomad_addr by default).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "alloy_version" {
  type    = string
  default = "1.15.1"
}

variable "nomad_addr" {
  type        = string
  default     = "127.0.0.1:4646"
  description = "Nomad agent endpoint each Alloy talks to. Defaults to the local agent: every Nomad client runs an agent on 127.0.0.1, so this scrape is local and works the same on every node in any datacenter."
}

variable "nomad_token" {
  type    = string
  default = "0ca13634-c413-c24b-627c-f6f1efbff721"
}

# Forwarding endpoints — resolved at template time via Consul service discovery.
# The defaults are Consul service names; if you want to point at an external
# Prometheus/Loki, set these to literal URLs and the template will use them as-is.
variable "prometheus_service_name" {
  type        = string
  default     = "abc-nodes-prometheus"
  description = "Consul service name for the Prometheus remote_write target."
}

variable "loki_service_name" {
  type        = string
  default     = "abc-nodes-loki"
  description = "Consul service name for the Loki push target."
}

job "abc-nodes-alloy" {
  namespace = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  # One Alloy per node — required so alloc logs on each client are tailed locally.
  type = "system"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "alloy"
  }

  group "alloy" {
    network {
      mode = "host"
      port "ui" {
        static = 12345
      }
    }

    task "alloy" {
      driver = "raw_exec"

      config {
        # chmod required because GitHub zip does not preserve execute bit
        command = "/bin/sh"
        args = [
          "-c",
          "chmod +x ${NOMAD_TASK_DIR}/alloy-linux-amd64 && exec ${NOMAD_TASK_DIR}/alloy-linux-amd64 run ${NOMAD_TASK_DIR}/config.alloy --server.http.listen-addr=0.0.0.0:12345 --storage.path=${NOMAD_TASK_DIR}/data",
        ]
      }

      artifact {
        source      = "https://github.com/grafana/alloy/releases/download/v${var.alloy_version}/alloy-linux-amd64.zip"
        destination = "local/"
      }

      template {
        # Re-render when Prometheus/Loki get rescheduled to a different node.
        change_mode = "restart"
        data        = <<EOF
// ── Host / node metrics ──────────────────────────────────────────────────────
prometheus.exporter.unix "host" {}

prometheus.scrape "host_metrics" {
  targets         = prometheus.exporter.unix.host.targets
  forward_to      = [prometheus.remote_write.local.receiver]
  job_name        = "node"
  scrape_interval = "30s"
}

// ── Nomad /v1/metrics — local agent on each node (system job).
prometheus.scrape "nomad_metrics" {
  targets = [{
    __address__      = "${var.nomad_addr}",
    __metrics_path__ = "/v1/metrics",
  }]
  params = {
    "format" = ["prometheus"],
  }
  bearer_token    = "${var.nomad_token}"
  scrape_interval = "30s"
  forward_to      = [prometheus.remote_write.local.receiver]
  job_name        = "nomad"
}

// ── Remote write → Prometheus (Consul-discovered) ─────────────────────────────
prometheus.remote_write "local" {
  endpoint {
{{- range service "${var.prometheus_service_name}" }}
    url = "http://{{ .Address }}:{{ .Port }}/api/v1/write"
{{- end }}
  }
}

// ── Nomad alloc log collection → Loki ────────────────────────────────────────
// Tail both common data_dir layouts (official Linux packages often use /opt/nomad/data).
local.file_match "nomad_alloc_logs_opt" {
  path_targets = [{__path__ = "/opt/nomad/data/alloc/*/alloc/logs/*.std*.*"}]
}
loki.source.file "nomad_logs_opt" {
  targets    = local.file_match.nomad_alloc_logs_opt.targets
  forward_to = [loki.process.add_labels.receiver]
}
local.file_match "nomad_alloc_logs_varlib" {
  path_targets = [{__path__ = "/var/lib/nomad/alloc/*/alloc/logs/*.std*.*"}]
}
loki.source.file "nomad_logs_varlib" {
  targets    = local.file_match.nomad_alloc_logs_varlib.targets
  forward_to = [loki.process.add_labels.receiver]
}

loki.process "add_labels" {
  // Extract alloc_id, task, stream from the "filename" label (the path forwarded
  // by loki.source.file). "__path__" is an internal discovery label not available
  // in the pipeline stage — "filename" is the correct source here.
  stage.regex {
    expression = "/alloc/(?P<alloc_id>[^/]+)/alloc/logs/(?P<task>[^.]+)\\.(?P<stream>std(?:out|err))\\."
    source     = "filename"
  }
  stage.labels {
    values = {
      alloc_id = "",
      task     = "",
      stream   = "",
    }
  }
  forward_to = [loki.write.local.receiver]
}

loki.write "local" {
  endpoint {
{{- range service "${var.loki_service_name}" }}
    url = "http://{{ .Address }}:{{ .Port }}/loki/api/v1/push"
{{- end }}
  }
}
EOF
        destination = "local/config.alloy"
      }

      resources {
        cpu    = 256
        memory = 256
      }

      service {
        name     = "abc-nodes-alloy"
        port     = "ui"
        provider = "consul"
        tags = [
          "abc-nodes", "alloy", "observability",
          # Alloy exposes Prometheus metrics on /metrics on its UI port.
          "prometheus.scrape=true",
          "traefik.enable=true",
          "traefik.http.routers.alloy.rule=Host(`alloy.aither`)",
          "traefik.http.routers.alloy.entrypoints=web",
          "traefik.http.services.alloy.loadbalancer.server.port=12345",
        ]

        check {
          name     = "alloy-health"
          type     = "http"
          path     = "/-/healthy"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
