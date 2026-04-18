# Grafana Alloy (observability agent) — abc-nodes floor
# Collects: host metrics (unix exporter), Nomad cluster metrics,
#           Nomad alloc logs. Ships metrics → Prometheus, logs → Loki.
# raw_exec so it runs on the host and can reach 127.0.0.1 for all
# bridge-mode services (Prometheus :9090, Loki :3100).

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

variable "prometheus_url" {
  type    = string
  default = "http://127.0.0.1:9090/api/v1/write"
}

variable "loki_url" {
  type    = string
  default = "http://127.0.0.1:3100/loki/api/v1/push"
}

variable "nomad_alloc_log_path" {
  type        = string
  description = "Glob path to Nomad allocation log files on the host."
  default     = "/var/lib/nomad/alloc/*/alloc/logs/*.std*.*"
}

job "abc-nodes-alloy" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "alloy"
  }

  group "alloy" {
    count = 1

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

// ── Nomad cluster metrics ─────────────────────────────────────────────────────
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
    url = "${var.prometheus_url}"
  }
}

// ── Nomad alloc log collection → Loki ────────────────────────────────────────
local.file_match "nomad_alloc_logs" {
  path_targets = [{__path__ = "${var.nomad_alloc_log_path}"}]
}

loki.source.file "nomad_logs" {
  targets    = local.file_match.nomad_alloc_logs.targets
  forward_to = [loki.process.add_labels.receiver]
}

loki.process "add_labels" {
  stage.regex {
    expression = "/alloc/(?P<alloc_id>[^/]+)/alloc/logs/(?P<task>[^.]+)\\.(?P<stream>std(?:out|err))\\."
    source     = "__path__"
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
    url = "${var.loki_url}"
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
