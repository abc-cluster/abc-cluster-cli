# Docs (static site) — abc-nodes floor
#
# Serves the Docusaurus build output for abc-cluster-cli at http://docs.aither.
#
# CONTENT SOURCE
# ──────────────
#  Static files live on aither's "scratch" host volume at
#  /opt/nomad/scratch/abc-docs (mounted into the container as /scratch/abc-docs).
#  Push new content with:
#    just docs-deploy
#  which runs deployments/abc-nodes/docs/deploy-docs.sh — builds the site
#  (just docs-build) and rsyncs website/build/ to the host volume.
#
# Caddy reads files on each request — content updates take effect immediately
# without restarting the job.  Restart only when changing the Caddyfile inline
# template below or other job-spec fields.
#
# ROUTING
# ───────
#  Consul-registered as `abc-nodes-docs` with Traefik tags binding the route
#  Host(`docs.aither`) → port 8080.  Both Caddy surfaces (system Caddyfile.lan
#  and experimental caddy-tailscale.nomad.hcl) include a `http://docs.aither`
#  vhost that reverse-proxies to abc-nodes-traefik.service.consul:8081.
#
# Deploy:
#   just docs-job-run
# or:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/abc-nodes-docs.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "caddy_image" {
  type    = string
  default = "caddy:2-alpine"
}

job "abc-nodes-docs" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "docs"
  }

  group "docs" {
    count = 1

    # Pin to aither: docs content lives on aither's scratch host volume.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        to = 8080
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = true
      source    = "scratch"
    }

    task "caddy" {
      driver = "containerd-driver"

      config {
        image = var.caddy_image
        args  = ["caddy", "run", "--config", "/local/Caddyfile", "--adapter", "caddyfile"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = true
      }

      template {
        destination = "local/Caddyfile"
        data        = <<-CADDYFILE
        {
          auto_https off
          admin off
        }

        :8080 {
          root * /scratch/abc-docs
          encode gzip
          try_files {path} {path}/ /index.html
          file_server
        }
        CADDYFILE
      }

      resources {
        cpu    = 100
        memory = 64
      }

      service {
        name     = "abc-nodes-docs"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "docs", "static",
          "traefik.enable=true",
          "traefik.http.routers.abc-docs.rule=Host(`docs.aither`)",
          "traefik.http.routers.abc-docs.entrypoints=web",
          # No loadbalancer.server.port tag: Traefik uses the dynamic host port
          # registered in Consul (bridge-mode allocs get a random host port that
          # forwards to container :8080).  Hardcoding 8080 here would point
          # Traefik at 127.0.0.1:8080 — which is tusd on this node.
        ]

        check {
          name     = "docs-http"
          type     = "http"
          path     = "/"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
