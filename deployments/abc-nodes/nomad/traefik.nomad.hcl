# Traefik reverse proxy — abc-nodes floor
#
# Role in the networking stack
# ─────────────────────────────
#  Browser → Caddy (*.aither vhosts, TLS, ForwardAuth)
#          → Traefik:8081 (Consul-catalog load balancer)
#          → service instances (on any node, resolved via Consul)
#
#  Caddy is the external entry point and handles:
#    • vhost routing, TLS termination (future), ForwardAuth on tusd
#    • landing page, /services/* redirects, Nomad/Consul UI passthrough
#  Traefik is the internal load balancer and handles:
#    • Consul catalog service discovery (reads routing tags from each service)
#    • Health-aware load balancing across instances on any node
#    • Automatic backend updates when services move or scale
#
#  Traefik dashboard: http://traefik.aither  (Caddy → port 8888)
#  Traefik web entry: port 8081              (Caddy proxies service vhosts here)
#
# Driver: raw_exec — runs directly on the host so it can bind to host ports
# and resolve <service>.service.consul via the host's dnsmasq.
#
# Port map:
#   8081 — web entry point (Caddy backends point here)
#   8888 — Traefik API / dashboard entry point

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "traefik_version" {
  type    = string
  default = "3.3.5"
}

job "abc-nodes-traefik" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "traefik"
  }

  group "traefik" {
    count = 1

    network {
      mode = "host"
      port "http" {
        static = 8081
      }
      port "dashboard" {
        static = 8888
      }
    }

    task "traefik" {
      driver = "raw_exec"

      config {
        command = "local/traefik"
        args    = ["--configFile=local/traefik.yml"]
      }

      artifact {
        source      = "https://github.com/traefik/traefik/releases/download/v${var.traefik_version}/traefik_v${var.traefik_version}_linux_amd64.tar.gz"
        destination = "local/"
      }

      # ── Static Traefik configuration ─────────────────────────────────────────
      template {
        destination = "local/traefik.yml"
        data        = <<EOF
global:
  checkNewVersion: false
  sendAnonymousUsage: false

# Dashboard exposed on the `traefik` entrypoint (port 8888).
# Caddy proxies traefik.aither → port 8888 for the UI.
api:
  dashboard: true
  insecure: true

# /ping on the traefik entrypoint — used by Consul health check.
ping:
  entryPoint: traefik

entryPoints:
  # Primary entry point: Caddy forwards all service vhost traffic here.
  web:
    address: ":8081"
  # Traefik API / dashboard entry point.
  traefik:
    address: ":8888"

providers:
  # Consul catalog: Traefik reads routing rules from Consul service tags.
  # Each service opts in with traefik.enable=true and sets its route/port via tags.
  # Backend address = Consul-registered IP:port (auto-updated when services move).
  consulCatalog:
    endpoint:
      address: "127.0.0.1:8500"
    prefix: "traefik"
    exposedByDefault: false
    # refreshInterval: how often Traefik polls Consul for service changes.
    refreshInterval: "15s"

  # File provider: static middleware definitions that cannot come from catalog tags.
  # ForwardAuth middleware points at abc-nodes-auth via Consul DNS — resolves
  # to the current host running the auth service on any node.
  file:
    filename: "local/middlewares.yml"
    watch: true

log:
  level: INFO
EOF
      }

      # ── Middleware definitions (file provider) ───────────────────────────────
      # ForwardAuth: validates Bearer / X-Nomad-Token headers via abc-nodes-auth.
      # Referenced by service tags as: nomad-auth@file
      # Note: tusd ForwardAuth is handled by Caddy; this is available for direct
      # Traefik access (port 8081) or future service-to-service auth.
      template {
        destination = "local/middlewares.yml"
        data        = <<EOF
http:
  middlewares:
    nomad-auth:
      forwardAuth:
        address: "http://abc-nodes-auth.service.consul:9191/auth"
        trustForwardHeader: true
        authResponseHeaders:
          - "X-Auth-User"
          - "X-Auth-Group"
          - "X-Auth-Namespace"
EOF
      }

      resources {
        cpu    = 256
        memory = 256
      }

      # Web entry point registered in Consul — Caddy uses this for all service vhosts.
      service {
        name     = "abc-nodes-traefik"
        port     = "http"
        provider = "consul"
        tags     = ["abc-nodes", "traefik", "proxy"]

        check {
          name     = "traefik-web-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }

      # Dashboard port registered separately — Caddy uses this for traefik.aither.
      service {
        name     = "abc-nodes-traefik-dashboard"
        port     = "dashboard"
        provider = "consul"
        tags     = ["abc-nodes", "traefik", "dashboard"]

        check {
          name     = "traefik-ping"
          type     = "http"
          path     = "/ping"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
