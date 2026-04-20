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
  type    = string
  default = "100.70.185.46:4646"
}

variable "nomad_token" {
  type    = string
  default = "0ca13634-c413-c24b-627c-f6f1efbff721"
}

# Full URLs; leave empty to derive from the host part of nomad_addr (same host as
# Nomad API is typical for single-node or tailnet single-observability-node setups).
variable "prometheus_url" {
  type        = string
  description = "Prometheus remote_write URL; empty = http://<nomad_addr host>:9090/api/v1/write"
  default     = ""
}

variable "loki_url" {
  type        = string
  description = "Loki push URL; empty = http://<nomad_addr host>:3100/loki/api/v1/push"
  default     = ""
}

locals {
  nomad_http_host = split(":", var.nomad_addr)[0]
  prometheus_remote_write_url = var.prometheus_url != "" ? var.prometheus_url : "http://${local.nomad_http_host}:9090/api/v1/write"
  loki_push_url                 = var.loki_url != "" ? var.loki_url : "http://${local.nomad_http_host}:3100/loki/api/v1/push"
}

job "abc-nodes-alloy" {
  namespace = "services"
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
        data        = <<EOF
// ── Host / node metrics ──────────────────────────────────────────────────────
prometheus.exporter.unix "host" {}

prometheus.scrape "host_metrics" {
  targets         = prometheus.exporter.unix.host.targets
  forward_to      = [prometheus.remote_write.local.receiver]
  job_name        = "node"
  scrape_interval = "30s"
}

// ── Nomad /v1/metrics (use nomad_addr; loopback often does not match bind_addr).
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

// ── Remote write → Prometheus ─────────────────────────────────────────────────
prometheus.remote_write "local" {
  endpoint {
    url = "${local.prometheus_remote_write_url}"
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
    url = "${local.loki_push_url}"
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
        provider = "nomad"
        tags     = ["abc-nodes", "alloy", "observability"]
      }
    }
  }
}
