# Caddy — Tailscale surface for abc-nodes (abc-experimental namespace)
#
# Purpose
# ───────
# Mirrors the routing of the system Caddy (Caddyfile.lan / caddy.service) but
# binds ONLY on the Tailscale IP (100.70.185.46).  The system Caddy retains
# the LAN IP (146.232.174.77).  Both listen on ports 80/443 without conflict
# because each is bound to a different IP.
#
# Use this job to iterate on routing rules, the landing page, and service links
# without touching the running system Caddy.  Once changes are verified here
# they can be copied back into Caddyfile.lan.
#
# Prerequisites
# ─────────────
#   1.  Update /etc/caddy/Caddyfile.lan (or caddy.service override) to use
#       lan_bind only (remove 100.70.185.46 from any_bind).  Otherwise port 80
#       on 100.70.185.46 will conflict and this job will fail to start.
#   2.  Ensure Traefik (abc-nodes-traefik) is running in abc-services.
#   3.  Consul must be reachable at 127.0.0.1:8500 for .service.consul DNS.
#
# Deploy / iterate
# ────────────────
#   abc admin services nomad cli -- job run \
#     deployments/abc-nodes/nomad/experimental/caddy-tailscale.nomad.hcl
#
# Reload config without restart (after editing this file and re-running above):
#   The template change_mode = "signal" + SIGUSR1 triggers a live Caddyfile
#   reload on template change.  A full job stop+start is needed for port changes.
#
# Ports
# ─────
#   80   HTTP (vhosts + redirect logic — no HTTPS for internal Tailscale routes)
#   443  HTTPS (reserved; auto_https disabled — enable when ACME is configured)
#   2020 Caddy Admin API (127.0.0.1:2020 — avoids conflict with system Caddy on 2019)

# ── Network config variables ──────────────────────────────────────────────────
# Override at deploy time with: nomad job run -var service_domain=X ...
# These are injected into the landing page CONFIG block at template render time.
variable "service_domain" {
  type        = string
  default     = "aither"
  description = "Subdomain base for all cluster services (e.g. nomad.aither, grafana.aither)"
}

variable "lan_host" {
  type        = string
  default     = "aither.mb.sun.ac.za"
  description = "Institutional LAN hostname shown in the footer"
}

variable "lan_ip" {
  type        = string
  default     = "146.232.174.77"
  description = "Institutional LAN IP of aither"
}

variable "ts_ip" {
  type        = string
  default     = "100.70.185.46"
  description = "Tailscale IP — used as split-DNS nameserver for *.{service_domain}"
}

job "abc-experimental-caddy-tailscale" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 60

  group "caddy" {
    count = 1

    # Pin to aither: Tailscale IP 100.70.185.46 is aither's tailnet address.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    # raw_exec: runs as a host process — full access to the host network stack
    # including the Tailscale interface (100.70.185.46).  containerd-driver
    # does not expose the Tailscale interface inside the container netns.
    # Nomad runs as root, so binding port 80 is allowed without extra capabilities.

    restart {
      attempts = 5
      delay    = "10s"
      interval = "2m"
      mode     = "delay"
    }

    task "caddy" {
      driver = "raw_exec"

      config {
        # /usr/bin/caddy is the system Caddy binary already present on aither.
        # $NOMAD_TASK_DIR is expanded by the shell; it points to this task's
        # local/ directory where the Caddyfile template is rendered.
        command = "/bin/sh"
        args    = ["-c", "exec /usr/bin/caddy run --config $NOMAD_TASK_DIR/Caddyfile --adapter caddyfile"]
      }

      # ── Caddyfile ────────────────────────────────────────────────────────────
      # Serves only the landing page on the Tailscale IP.
      # Services are linked directly by IP:port — no vhost routing needed.
      # Edit and re-run the job to iterate.
      template {
        destination   = "/local/Caddyfile"
        change_mode   = "signal"
        change_signal = "SIGUSR1"

        data = <<-CADDYFILE
# Architecture
# ────────────
# Single Caddy process (Nomad raw_exec) owns port 80 on BOTH network surfaces:
#   *.aither (100.70.185.46)  — Tailscale split-DNS; domain-mode links
#   aither.mb.sun.ac.za / 146.232.174.77  — institutional LAN; subpath-mode links
#
# Routing chain: Caddy → Traefik:8081 (Consul-catalog LB) → service instances.
# All abc-services run on aither; Consul is used for health-check-aware DNS.
{
  auto_https disable_redirects
  # Port 2020: avoids conflict with systemd Caddy admin socket on 2019.
  admin 127.0.0.1:2020
}

# ── Bind snippets ─────────────────────────────────────────────────────────────
(ts_bind) {
  bind 100.70.185.46
}
(lan_bind) {
  bind 146.232.174.77
}
(both_bind) {
  bind 100.70.185.46 146.232.174.77
}

# ══ SERVICE VHOSTS (*.aither) — Tailscale domain mode ════════════════════════
# Tailscale split-DNS routes *.aither → 100.70.185.46.
# Routing: Caddy (Host match) → Traefik:8081 (Consul LB) → live backend.

http://grafana.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://prometheus.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://loki.aither {
  import ts_bind
  @root path /
  redir @root /ready 308
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://alloy.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://ntfy.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://uppy.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

# MinIO vhosts redirect to RustFS (cluster switched entirely to RustFS).
http://minio.aither {
  import ts_bind
  redir * http://rustfs.aither{uri} 308
}
http://minio-console.aither {
  import ts_bind
  redir * http://rustfs.aither/rustfs/console/ 308
}

http://rustfs.aither {
  import ts_bind
  # RustFS console UI lives at /rustfs/console/ on port 9901.
  # It MUST be served under the same hostname as the S3 API (rustfs.aither)
  # because the console JS auto-detects serverHost from window.location.host
  # and uses it as the STS endpoint. If served on a separate vhost the STS
  # call goes to the console port instead of the S3 port → login fails.
  @console path /rustfs/console/*
  handle @console {
    reverse_proxy abc-nodes-rustfs-console.service.consul:9901
  }
  # S3 API catch-all
  handle {
    reverse_proxy abc-nodes-traefik.service.consul:8081
  }
}

http://rustfs-console.aither {
  import ts_bind
  # Keep old hostname working — redirect to the correct path under rustfs.aither.
  redir * http://rustfs.aither/rustfs/console/ 308
}

# ── Garage — long-term archive + backup tier ──────────────────────────────────
# S3 API on port 3900. No console-on-different-host login problem (Garage
# admin is bearer-token over the admin API, not a host-bound SPA), so we
# don't need the @s3_signed gymnastics RustFS needs.
# Tailscale-only for now — Garage is consumed server-side (restic, fx-archive)
# via Consul DNS; LAN browser access can be added later if required.
http://garage.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://garage-webui.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

# ── NATS — messaging + JetStream (abc-experimental) ──────────────────────────
# Monitoring HTTP on port 8222 only.  The NATS protocol port 4222 is TCP
# (not HTTP), so clients connect direct to the Tailscale/LAN IP at :4222 —
# Caddy doesn't proxy it.  This vhost is the dashboard / /jsz / /healthz
# surface, registered in Consul with a Traefik tag in the nats jobspec.
http://nats.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

# ── GitRiver — self-hosted Git platform (abc-experimental) ────────────────────
# HTTP on port 3030 (NOT 3000 — that's Grafana).  Tailscale surface only.
# Routed direct to the Tailscale IP, bypassing Traefik — same pattern as
# consul.aither / nomad.aither.
http://gitriver.aither {
  import ts_bind
  reverse_proxy 100.70.185.46:3030
}

http://docs.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://vault.aither {
  import ts_bind
  @root path /
  redir @root /ui/ 308
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://boundary.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

# tusd — ForwardAuth applied by Caddy; preflight + Uppy-Referer bypass auth.
http://tusd.aither {
  import ts_bind

  @preflight method OPTIONS
  handle @preflight {
    reverse_proxy abc-nodes-traefik.service.consul:8081
  }

  @from_uppy header Referer *uppy.aither*
  handle @from_uppy {
    reverse_proxy abc-nodes-traefik.service.consul:8081
  }

  handle {
    forward_auth abc-nodes-auth.service.consul:9191 {
      uri /auth
      copy_headers X-Auth-User X-Auth-Group X-Auth-Namespace
    }
    reverse_proxy abc-nodes-traefik.service.consul:8081
  }
}

# Direct proxies — bypass Traefik (single-node / bootstrapping concern).
http://traefik.aither {
  import ts_bind
  @root path /
  redir @root /dashboard/ 308
  reverse_proxy abc-nodes-traefik-dashboard.service.consul:8888
}

http://consul.aither {
  import ts_bind
  @root path /
  redir @root /ui/ 308
  reverse_proxy 100.70.185.46:8500
}

http://nomad.aither {
  import ts_bind
  @root path /
  redir @root /ui/settings/tokens 308
  reverse_proxy 100.70.185.46:4646
}

# ══ LANDING PAGE + SUBPATH ROUTING (both networks) ═══════════════════════════
# Serves the landing page and all service subpaths.
# Binds on BOTH IPs so LAN users (aither.mb.sun.ac.za / 146.232.174.77) and
# Tailscale users accessing by bare IP (100.70.185.46) get the same page.
# The landing page JS auto-detects domain vs subpath mode from window.location.
#
# Subpath routes mirror *.aither vhosts:
#   /grafana/*           → grafana.aither  (no prefix strip; serve_from_sub_path=true)
#   /prometheus/*        → prometheus.aither (strip prefix)
#   ... etc ...
# ntfy /v1/* API calls are routed via Referer header before Nomad's /v1/* catch.

http://aither.mb.sun.ac.za,
http://146.232.174.77,
http://100.70.185.46 {
  import both_bind

  # ── RustFS S3 — signed requests + STS (must precede @root) ──────────────
  # Browser STS calls are POST / with Action=... in the form BODY (not query
  # string), but they ALWAYS carry an AWS4-HMAC-SHA256 Authorization header.
  # Match on that header to capture both STS login and post-login signed S3
  # API / admin calls. Console UI assets are excluded (they have no signing).
  # Must come before @root so that POST to / doesn't hit the file_server (405).
  @s3_signed {
    header Authorization "AWS4-HMAC-SHA256 *"
    not path /rustfs/console/*
  }
  handle @s3_signed {
    reverse_proxy abc-nodes-rustfs-s3.service.consul:9900
  }

  # ── Root landing page ────────────────────────────────────────────────────
  @root path /
  handle @root {
    root * {{ env "NOMAD_TASK_DIR" }}/landing
    rewrite * /index.html
    file_server
  }

  # ── Nomad UI — SPA rootURL hardcoded to /ui/ ───────────────────────────
  handle /ui/* {
    reverse_proxy 100.70.185.46:4646
  }

  # ── ntfy API (/v1/*) — before Nomad /v1/* catch-all ────────────────────
  # ntfy's web app makes API calls to /v1/* on the same origin. Route by
  # Referer to separate ntfy API from Nomad API on the same path prefix.
  @ntfy_api {
    path /v1/*
    header Referer *aither.mb.sun.ac.za/ntfy*
  }
  handle @ntfy_api {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host ntfy.aither
    }
  }

  @ntfy_misc {
    path /metrics /ws /v1/stats /v1/health
    header Referer *aither.mb.sun.ac.za/ntfy*
  }
  handle @ntfy_misc {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host ntfy.aither
    }
  }

  handle /v1/* {
    reverse_proxy 100.70.185.46:4646
  }

  # ── tusd /files/* ────────────────────────────────────────────────────────
  # Auth flow: ForwardAuth gates /files/ so the bucket isn't world-writable.
  # The Uppy SPA can't sign every request with a Nomad token (it's a static
  # page, no session), so we bypass auth for requests that are unambiguously
  # part of the tus protocol — identified by either:
  #
  #   1. OPTIONS preflight  — CORS check; browsers send these unauthenticated
  #      and they have no body, so they're safe to pass through.
  #   2. `Tus-Resumable` header — REQUIRED by the tus protocol on every
  #      Client request (spec §2). Plain curl / drive-by probes don't set
  #      it, so this cleanly distinguishes a real upload client from
  #      anything else.
  #
  # The previous `header Referer *uppy.aither*` bypass was both too narrow
  # (didn't match the LAN URL http://146.232.174.77/uppy/) and redundant —
  # the Tus-Resumable check covers every real upload regardless of which
  # surface the page was loaded from.
  @files_preflight {
    method OPTIONS
    path /files/*
  }
  handle @files_preflight {
    reverse_proxy abc-nodes-tusd.service.consul:8080
  }
  @files_from_tus_client {
    path /files/*
    header Tus-Resumable *
  }
  handle @files_from_tus_client {
    reverse_proxy abc-nodes-tusd.service.consul:8080
  }
  handle /files/* {
    forward_auth abc-nodes-auth.service.consul:9191 {
      uri /auth
      copy_headers X-Auth-User X-Auth-Group X-Auth-Namespace
    }
    reverse_proxy abc-nodes-tusd.service.consul:8080
  }

  # ── Service subpath proxies ───────────────────────────────────────────────
  # Grafana: GF_SERVER_SERVE_FROM_SUB_PATH=true — do NOT strip /grafana prefix;
  # Grafana handles it internally. header_down rewrites root-relative redirects.
  handle /grafana/* {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host grafana.aither
      header_down Location "^(/login.*)$" "/grafana$1"
      header_down Location "^(/logout.*)$" "/grafana$1"
    }
  }
  handle /services/grafana/* {
    uri strip_prefix /services
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host grafana.aither
      header_down Location "^(/login.*)$" "/grafana$1"
      header_down Location "^(/logout.*)$" "/grafana$1"
    }
  }

  handle /prometheus/* {
    uri strip_prefix /prometheus
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host prometheus.aither
    }
  }
  handle /services/prometheus/* {
    uri strip_prefix /services/prometheus
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host prometheus.aither
    }
  }

  handle /loki/* {
    @loki_root path /loki/
    redir @loki_root /loki/ready 308
    uri strip_prefix /loki
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host loki.aither
    }
  }
  handle /services/loki/* {
    @sloki_root path /services/loki/
    redir @sloki_root /services/loki/ready 308
    uri strip_prefix /services/loki
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host loki.aither
    }
  }

  handle /alloy/* {
    uri strip_prefix /alloy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host alloy.aither
    }
  }
  handle /services/alloy/* {
    uri strip_prefix /services/alloy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host alloy.aither
    }
  }

  handle /ntfy/* {
    uri strip_prefix /ntfy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host ntfy.aither
    }
  }
  handle /services/ntfy/* {
    uri strip_prefix /services/ntfy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host ntfy.aither
    }
  }

  handle /uppy/* {
    uri strip_prefix /uppy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host uppy.aither
    }
  }
  handle /services/uppy/* {
    uri strip_prefix /services/uppy
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host uppy.aither
    }
  }

  # MinIO subpaths → redirect to RustFS equivalents.
  handle /minio-console/* {
    redir * http://146.232.174.77:9910/rustfs/console/ 308
  }
  handle /services/minio-console/* {
    redir * http://146.232.174.77:9910/rustfs/console/ 308
  }
  handle /minio/* {
    redir * http://146.232.174.77:9910{path} 308
  }
  handle /services/minio/* {
    redir * http://146.232.174.77:9910{path} 308
  }

  # RustFS — console path must NOT have prefix stripped (SPA base = /rustfs/console/).
  # Route directly to the console service; catch-all strips /rustfs for the S3 API.
  handle /rustfs/console/* {
    reverse_proxy abc-nodes-rustfs-console.service.consul:9901
  }
  handle /services/rustfs/console/* {
    uri strip_prefix /services
    reverse_proxy abc-nodes-rustfs-console.service.consul:9901
  }
  handle /rustfs/* {
    uri strip_prefix /rustfs
    reverse_proxy abc-nodes-rustfs-s3.service.consul:9900
  }
  handle /services/rustfs/* {
    uri strip_prefix /services/rustfs
    reverse_proxy abc-nodes-rustfs-s3.service.consul:9900
  }

  handle /vault/* {
    uri strip_prefix /vault
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host vault.aither
    }
  }
  handle /services/vault/* {
    uri strip_prefix /services/vault
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host vault.aither
    }
  }

  handle /boundary/* {
    uri strip_prefix /boundary
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host boundary.aither
    }
  }
  handle /services/boundary/* {
    uri strip_prefix /services/boundary
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host boundary.aither
    }
  }

  # Nomad — direct proxy (Ember SPA; /ui/* handled above)
  handle /nomad/* {
    uri strip_prefix /nomad
    reverse_proxy 100.70.185.46:4646
  }
  handle /services/nomad/* {
    uri strip_prefix /services/nomad
    reverse_proxy 100.70.185.46:4646
  }

  # Consul — direct proxy
  handle /consul/* {
    uri strip_prefix /consul
    reverse_proxy 100.70.185.46:8500
  }
  handle /services/consul/* {
    uri strip_prefix /services/consul
    reverse_proxy 100.70.185.46:8500
  }

  # Traefik dashboard — direct proxy
  handle /traefik/* {
    uri strip_prefix /traefik
    reverse_proxy abc-nodes-traefik-dashboard.service.consul:8888
  }
  handle /services/traefik/* {
    uri strip_prefix /services/traefik
    reverse_proxy abc-nodes-traefik-dashboard.service.consul:8888
  }

  # Docs (Docusaurus — built with baseUrl=/, so its HTML references
  # absolute asset paths /assets/* and /img/*, which are NOT under /docs/*.
  # We forward all four roots to the docs job so the LAN subpath link
  # /docs/ renders fully styled (CSS, JS, favicon, logo).  The proper
  # long-term fix is to rebuild Docusaurus with baseUrl=/docs/ — at which
  # point the /assets and /img handlers below can be removed.
  handle /docs/* {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host docs.aither
    }
  }
  handle /assets/* {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host docs.aither
    }
  }
  handle /img/* {
    reverse_proxy abc-nodes-traefik.service.consul:8081 {
      header_up Host docs.aither
    }
  }

  # ── Fallback ──────────────────────────────────────────────────────────────
  handle {
    respond "Not Found" 404
  }
}
CADDYFILE
      }

      # ── Landing page ─────────────────────────────────────────────────────────
      # Served from /local/landing/index.html by the aither.mb.sun.ac.za vhost.
      # Source of truth lives in deployments/abc-nodes/caddy/landing/index.html
      # so we don't have to fight HCL2's heredoc parser (it would otherwise
      # mis-interpret CSS `@keyframes 100%{...}` as a `%{...}` directive).
      # Edit the file, re-run the job, Caddy SIGUSR1-reloads.
      template {
        destination   = "/local/landing/index.html"
        change_mode   = "signal"
        change_signal = "SIGUSR1"

        data = file(abspath("deployments/abc-nodes/caddy/landing/index.html"))
      }


      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
