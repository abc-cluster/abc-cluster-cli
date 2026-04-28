# NATS — messaging + JetStream persistence (abc-experimental namespace)
#
# WHAT IT IS
# ──────────
#  NATS is a high-perf pub/sub messaging system; JetStream is its persistent
#  streaming subsystem (durable streams, KV, object stores).  Single-node
#  deploy here — single binary, ~50MB image, sub-second startup.
#  https://nats.io  ·  https://docs.nats.io/jetstream
#
# ROLE IN THE CLUSTER
# ───────────────────
#  Initial use cases:
#   - Job event bus replacing ad-hoc HTTP POSTs (job-notifier, fx-* hooks)
#   - Durable streams for compute-job lifecycle events (queue → run → finish)
#   - KV store for transient cluster state (e.g. per-node lease tokens)
#   - Optional ntfy backend (ntfy can deliver via JetStream)
#
# DATA PERSISTENCE
# ────────────────
#  /scratch/nats-data on aither — JetStream's durable file store.  Survives
#  restarts.  Capped at 10 GiB on disk + 256 MiB in memory by default; bump
#  via the `nats_*` HCL vars when streams grow.
#
# ENDPOINTS
# ─────────
#  Client (NATS proto): nats://100.70.185.46:4222   (Tailscale)
#                       nats://aither.mb.sun.ac.za:4222   (LAN)
#                       abc-experimental-nats.service.consul:4222   (in-cluster)
#  Monitoring (HTTP)  : http://nats.aither/                (Tailscale, via Caddy)
#                       http://100.70.185.46:8222/         (direct)
#
# AUTH
# ────
#  No auth on initial deploy — cluster-internal use only.  Add NKEYs / mTLS
#  via the [accounts] / [authorization] config blocks when the service moves
#  out of abc-experimental.  See https://docs.nats.io/running-a-nats-service/configuration/securing_nats
#
# MONITORING UI
# ─────────────
#  The monitoring port (8222) serves a built-in JSON API and a small web
#  dashboard at /.  Useful URLs:
#   /varz       — runtime stats
#   /jsz        — JetStream account / stream / consumer state
#   /connz      — active connections
#   /healthz    — liveness probe (200 = ok)

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "nats_image" {
  type    = string
  default = "nats:2.10-alpine"
}

variable "nats_server_name" {
  type        = string
  default     = "abc-experimental-nats-1"
  description = "Server name used in cluster topology and monitoring."
}

variable "nats_client_port" {
  type    = number
  default = 4222
}

variable "nats_monitoring_port" {
  type    = number
  default = 8222
}

variable "nats_jetstream_max_memory" {
  type        = string
  default     = "256MB"
  description = "JetStream in-memory store cap (per-node)."
}

variable "nats_jetstream_max_file" {
  type        = string
  default     = "10GB"
  description = "JetStream disk-store cap (per-node)."
}

job "abc-experimental-nats" {
  namespace   = "abc-experimental"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"
  priority    = 60

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "nats"
  }

  group "nats" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "client" {
        static = var.nats_client_port
        to     = 4222
      }
      port "monitor" {
        static = var.nats_monitoring_port
        to     = 8222
      }
    }

    restart {
      attempts = 3
      delay    = "20s"
      interval = "5m"
      mode     = "delay"
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # Pre-create the JetStream store dir.  NATS image runs as the `nats` user
    # (uid 1000 in the alpine variant); chmod 0777 sidesteps host-vs-container
    # uid mismatches without requiring us to chown into the container's user.
    task "ensure-data-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args = [
          "mkdir -p /scratch/nats-data && chmod 0777 /scratch/nats-data",
        ]
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

    task "nats" {
      driver = "containerd-driver"

      config {
        image = var.nats_image
        # The nats image's default entrypoint is `nats-server`; we point it at
        # our rendered config file rather than passing a long flag list.
        args = ["-c", "/local/nats.conf"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      template {
        destination = "local/nats.conf"
        change_mode = "restart"
        data        = <<-CONF
        # NATS server config for abc-experimental — JetStream enabled,
        # cluster-internal-only access (no auth on initial deploy).
        server_name: "${var.nats_server_name}"
        host:        "0.0.0.0"
        port:        4222
        http_port:   8222

        # Generous defaults — tighten these (and add lame_duck_*) once we
        # have real load profiles.
        max_payload: 8MB
        max_pending: 256MB

        jetstream {
            store_dir:        "/scratch/nats-data"
            max_memory_store: ${var.nats_jetstream_max_memory}
            max_file_store:   ${var.nats_jetstream_max_file}
        }

        # Logging — stdout so Nomad captures it.  No timestamps because Nomad
        # already wraps each line with its own.
        logtime: false
        debug:   false
        trace:   false
        CONF
      }

      resources {
        cpu    = 500
        memory = 512
      }

      service {
        name     = "abc-experimental-nats"
        port     = "client"
        provider = "consul"
        tags     = ["abc-experimental", "nats", "jetstream", "messaging"]

        check {
          name     = "nats-client-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }

      service {
        name     = "abc-experimental-nats-monitor"
        port     = "monitor"
        provider = "consul"
        tags = [
          "abc-experimental", "nats", "monitor",
          "traefik.enable=true",
          "traefik.http.routers.nats.rule=Host(`nats.aither`)",
          "traefik.http.routers.nats.entrypoints=web",
          "traefik.http.services.nats.loadbalancer.server.port=8222",
        ]

        check {
          name     = "nats-monitor-http"
          type     = "http"
          path     = "/healthz"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
