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
  default = ["*"]
}

variable "alloy_version" {
  type    = string
  default = "1.15.1"
}

# Note: we don't expose a `nomad_addr` var anymore. Each Alloy uses the host's
# primary IP via `${attr.unique.network.ip-address}:4646` rendered at task
# run time (see config.alloy heredoc). Hardcoding 127.0.0.1 doesn't work on
# this fleet because most Nomad clients bind only to their Tailscale/primary
# IP, not loopback, and each node's IP is different — this resolves both.

variable "nomad_token" {
  type    = string
  default = "0ca13634-c413-c24b-627c-f6f1efbff721"
}

# Forwarding endpoints — STATIC URLs.  Alloy is a `system` job that runs on
# every Nomad client across every datacenter; many of those nodes don't run a
# Consul agent (Consul is centralised on aither for this cluster), so we
# can't use consul-template here without breaking placement on Consul-less
# nodes.  Terraform overrides these defaults with the central cluster IP.
variable "prometheus_url" {
  type        = string
  default     = "http://100.70.185.46:8428/api/v1/write"
  description = "Prometheus remote_write endpoint reachable from EVERY node (typically the central cluster IP)."
}

variable "loki_url" {
  type        = string
  default     = "http://100.70.185.46:9428/insert/loki/api/v1/push"
  description = "Loki push endpoint reachable from EVERY node."
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

  # Parallel rollout — the default for system jobs is one-at-a-time, which
  # takes ~minutes-per-node × N-nodes to finish a config rollout across the
  # fleet.  Bumping to half-the-fleet at a time keeps the dashboard fresh
  # during updates without compromising availability (Alloy is idempotent +
  # push-only, so a brief gap is fine).
  update {
    max_parallel      = 6
    min_healthy_time  = "10s"
    healthy_deadline  = "2m"
    progress_deadline = "5m"
  }

  group "alloy" {
    # Skip spot/preemptible instances. These churn frequently — every
    # preemption costs a full Alloy restart cycle and creates noise in the
    # dashboards (a fleet of stuttering host_metrics series). Their workloads
    # are typically batch and short-lived, so losing per-host visibility is
    # acceptable for now.
    #
    # TODO(observability/spot): revisit if/when spot becomes the dominant
    # workload tier — at that point we may want Alloy on spot too, with
    # a different identity convention (e.g. drop the per-instance host label
    # so series don't fragment on every preemption) and possibly a shorter
    # remote_write backoff so a 30-second-lived instance still posts data.
    constraint {
      attribute = "${node.class}"
      operator  = "!="
      value     = "gcp-spot"
    }

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
          "chmod +x ${NOMAD_TASK_DIR}/alloy-linux-${attr.cpu.arch} && exec ${NOMAD_TASK_DIR}/alloy-linux-${attr.cpu.arch} run ${NOMAD_TASK_DIR}/config.alloy --server.http.listen-addr=0.0.0.0:12345 --storage.path=${NOMAD_TASK_DIR}/data",
        ]
      }

      artifact {
        source      = "https://github.com/grafana/alloy/releases/download/v${var.alloy_version}/alloy-linux-${attr.cpu.arch}.zip"
        destination = "local/"
      }

      # Per-node host IP — used inside the consul-template-rendered config below
      # to point Alloy's Nomad scrape at THIS node's local agent. Most Nomad
      # clients in this fleet bind only to their primary IP, not 127.0.0.1, so
      # we can't hardcode loopback. ${attr.unique.network.ip-address} gets
      # substituted by Nomad when the task is placed (per-alloc, per-node).
      env {
        HOST_IP = "${attr.unique.network.ip-address}"
        HOST_DC = "${node.datacenter}"
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
//   {{ env "HOST_IP" }} is rendered by Nomad's consul-template at template
//   evaluation time; HOST_IP is set in the task `env` stanza above to
//   ${attr.unique.network.ip-address} (per-alloc HCL2 interpolation).
prometheus.scrape "nomad_metrics" {
  targets = [{
    __address__      = "{{ env "HOST_IP" }}:4646",
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

// ── Remote write → Prometheus (static URL — works on Consul-less nodes) ─────
prometheus.remote_write "local" {
  endpoint {
    url = "${var.prometheus_url}"
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
    url = "${var.loki_url}"
  }
  external_labels = {
    host       = "{{ env "NOMAD_NODE_NAME" }}",
    datacenter = "{{ env "HOST_DC" }}",
  }
}
EOF
        destination = "local/config.alloy"
      }

      resources {
        cpu    = 256
        memory = 256
      }

      # Service provider = "nomad" (NOT consul) so this job places on every
      # node in every datacenter regardless of whether the node runs a Consul
      # agent.  When provider = "consul", Nomad auto-injects an implicit
      # constraint `attr.consul.version >= 1.8.0` which silently filters out
      # GCP / OCI / on-prem nodes that don't have Consul — exactly what we
      # don't want for a `system` observability daemon.
      #
      # Alloy is push-only (remote_write to Prometheus, push to Loki) so it
      # doesn't need to be reachable via discovery from outside; this service
      # block is just for visibility in `nomad service list`.
      service {
        name     = "abc-nodes-alloy"
        port     = "ui"
        provider = "nomad"
        tags     = ["abc-nodes", "alloy", "observability"]

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
