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
  @files_preflight {
    method OPTIONS
    path /files/*
  }
  handle @files_preflight {
    reverse_proxy abc-nodes-tusd.service.consul:8080
  }
  @files_from_uppy {
    path /files/*
    header Referer *uppy.aither*
  }
  handle @files_from_uppy {
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
      # Edit here and re-run the job to update without touching system Caddy files.
      template {
        destination   = "/local/landing/index.html"
        change_mode   = "signal"
        change_signal = "SIGUSR1"

        data = <<-LANDING_HTML
<!DOCTYPE html>
<html data-theme="dark" data-system="grid" lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>abc-cluster · gateway</title>
  <meta name="description" content="African Bioinformatics Computing Cluster — install the abc CLI, explore services, submit jobs.">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet">
  <style>
    *,*::before,*::after{box-sizing:border-box;}
    html,body{margin:0;padding:0;}

  :root[data-system="grid"][data-theme="dark"] {
    --bg:#070f0c;--bg-1:#0b1a15;--bg-2:#0f2219;
    --text:#c5e5dc;--text-dim:#7db8a8;--text-mute:#4a7d6f;
    --ink:#e8f9f4;--ink-dim:#c8a84c;--accent:#c8a84c;
    --rule:#163d32;--rule-soft:#0e2a22;
    --selection:rgba(200,168,76,0.22);
    --brush-grad-start:#c8a84c;--brush-grad-end:#a87c2c;
  }
  :root[data-system="grid"][data-theme="light"] {
    --bg:#f0faf7;--bg-1:#e5f5f0;--bg-2:#d8ede7;
    --text:#1a4a3a;--text-dim:#2d7060;--text-mute:#6aaa96;
    --ink:#0a2820;--ink-dim:#907028;--accent:#907028;
    --rule:#a8d8cc;--rule-soft:#c8e8e0;
    --selection:rgba(144,112,40,0.22);
    --brush-grad-start:#907028;--brush-grad-end:#c8a84c;
  }
  :root[data-system="brush"][data-theme="dark"] {
    --bg:#100e0b;--bg-1:#1a1610;--bg-2:#231e14;
    --text:#d8c4a0;--text-dim:#a8865a;--text-mute:#6b5236;
    --ink:#f5e8cc;--ink-dim:#d4923a;--accent:#d4923a;
    --rule:#3a2e1e;--rule-soft:#2a2218;
    --selection:rgba(212,146,58,0.22);
    --brush-grad-start:#d4923a;--brush-grad-end:#e8b86d;
  }
  :root[data-system="brush"][data-theme="light"] {
    --bg:#faf7f0;--bg-1:#f2ece0;--bg-2:#e8deca;
    --text:#4a3520;--text-dim:#7a5830;--text-mute:#b08860;
    --ink:#1e1208;--ink-dim:#a06820;--accent:#c07828;
    --rule:#d8c09a;--rule-soft:#e4d4b8;
    --selection:rgba(192,120,40,0.22);
    --brush-grad-start:#c07828;--brush-grad-end:#d4923a;
  }
  .abc-landing *,
  .abc-landing *::before,
  .abc-landing *::after { box-sizing:border-box; }
  .abc-landing {
    background:var(--bg);color:var(--text);
    font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
    font-size:14px;line-height:1.55;
    font-feature-settings:'ss02','ss03';
    -webkit-font-smoothing:antialiased;
    min-height:100vh;
  }
  .abc-landing ::selection{background:var(--selection);color:var(--ink);}
  .abc-landing a{color:var(--ink);text-decoration:none;border-bottom:1px dotted var(--ink-dim);}
  .abc-landing a:hover{color:var(--accent);border-bottom-color:var(--accent);}
  .abc-landing .topbar{border-bottom:1px solid var(--rule);background:var(--bg);position:sticky;top:0;z-index:10;backdrop-filter:blur(6px);}
  .abc-landing .topbar-inner{max-width:1180px;margin:0 auto;padding:14px 32px;display:flex;align-items:center;gap:12px;}
  .abc-landing .brand{display:flex;align-items:center;gap:10px;font-weight:600;color:var(--ink);border:none;}
  .abc-landing .brand-mark{width:22px;height:22px;}
  .abc-landing .brand-name{letter-spacing:-0.01em;}
  .abc-landing .brand-name .dim{color:var(--text-dim);}
  .abc-landing .top-spacer{flex:1;}
  .abc-landing .top-link{display:inline-flex;align-items:center;gap:8px;padding:7px 12px;border-radius:4px;border:1px solid var(--rule);background:var(--bg-1);color:var(--text);font-size:13px;font-weight:500;letter-spacing:-0.01em;text-decoration:none;transition:color .12s,border-color .12s,background .12s;}
  .abc-landing .top-link:hover{color:var(--ink);border-color:var(--ink-dim);background:var(--bg-2);}
  .abc-landing .top-link-ico{width:18px;height:18px;display:block;color:var(--ink);}
  .abc-landing .top-link:hover .top-link-ico{color:var(--accent);}
  .abc-landing .theme-toggle{appearance:none;cursor:pointer;width:38px;height:38px;display:inline-flex;align-items:center;justify-content:center;border:1px solid var(--rule);border-radius:4px;background:var(--bg-1);color:var(--ink);position:relative;transition:border-color .12s,background .12s,color .12s;}
  .abc-landing .theme-toggle:hover{border-color:var(--ink-dim);color:var(--accent);background:var(--bg-2);}
  .abc-landing .theme-toggle .ti{width:22px;height:22px;position:absolute;inset:0;margin:auto;transition:opacity 220ms ease,transform 320ms cubic-bezier(.4,0,.2,1);}
  :root[data-theme="dark"] .abc-landing .theme-toggle .ti-sun{opacity:0;transform:scale(.6) rotate(-30deg);}
  :root[data-theme="dark"] .abc-landing .theme-toggle .ti-moon{opacity:1;transform:scale(1) rotate(0);}
  :root[data-theme="light"] .abc-landing .theme-toggle .ti-sun{opacity:1;transform:scale(1) rotate(0);}
  :root[data-theme="light"] .abc-landing .theme-toggle .ti-moon{opacity:0;transform:scale(.6) rotate(30deg);}
  @media(prefers-reduced-motion:reduce){.abc-landing .theme-toggle .ti{transition:opacity 1ms;{{"}}"}}
  .abc-landing .seg{display:inline-flex;border:1px solid var(--rule);border-radius:4px;overflow:hidden;}
  .abc-landing .seg button{appearance:none;background:transparent;border:none;cursor:pointer;color:var(--text-dim);font-family:inherit;font-size:11px;font-weight:500;padding:7px 12px;letter-spacing:.14em;text-transform:uppercase;border-right:1px solid var(--rule);}
  .abc-landing .seg button:last-child{border-right:none;}
  .abc-landing .seg button:hover{color:var(--ink);}
  .abc-landing .seg button.active{background:var(--bg-2);color:var(--ink);}
  .abc-landing .page{max-width:1180px;margin:0 auto;padding:0 32px;}
  .abc-landing .hero{display:flex;flex-direction:column;gap:36px;padding:64px 0 56px;border-bottom:1px solid var(--rule);}
  .abc-landing .hero-headline{display:grid;grid-template-columns:auto minmax(0,1fr);gap:32px;align-items:center;}
  .abc-landing .hero-headline-text{min-width:0;}
  .abc-landing .hero-cols{display:block;}
  .abc-landing .hero-mark{position:relative;aspect-ratio:1/1;width:clamp(150px, 18vw, 220px);flex-shrink:0;}
  .abc-landing .hero-mark svg{width:100%;height:100%;display:block;}
  .abc-landing .hero-mark::after{content:'';position:absolute;inset:-10px;background:linear-gradient(var(--ink-dim),var(--ink-dim)) top left/14px 1px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) top left/1px 14px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) top right/14px 1px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) top right/1px 14px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) bottom left/14px 1px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) bottom left/1px 14px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) bottom right/14px 1px no-repeat,linear-gradient(var(--ink-dim),var(--ink-dim)) bottom right/1px 14px no-repeat;pointer-events:none;opacity:.55;}
  .abc-landing .hero-body{padding-top:6px;}
  .abc-landing .eyebrow{color:var(--ink);font-size:11px;letter-spacing:.22em;text-transform:uppercase;margin-bottom:18px;}
  .abc-landing .eyebrow .dot{color:var(--accent);}
  .abc-landing h1{font-size:34px;line-height:1.15;font-weight:600;letter-spacing:-.02em;margin:0 0 18px;color:var(--ink);text-wrap:balance;}
  .abc-landing .acronym-h1{font-size:clamp(28px,5.2vw,58px);line-height:1.05;margin:0 0 10px;}
  .abc-landing .acronym-line{display:flex;flex-wrap:wrap;align-items:baseline;gap:2px 14px;color:var(--text);}
  .abc-landing .acronym-line .ac.ac-a{flex-basis:100%;margin-right:0;}
  .abc-landing .acronym-line .ac:not(.ac-a),.abc-landing .acronym-line>.ac-rest{font-size:0.46em;letter-spacing:0.01em;}
  .abc-landing .ac{display:inline-flex;align-items:baseline;}
  .abc-landing .ac-letter{color:var(--ink);font-weight:700;font-size:1.05em;letter-spacing:-.02em;}
  .abc-landing .ac-rest{color:var(--text);font-weight:500;}
  .abc-landing .ac-rotator{display:inline-block;height:1.18em;overflow:hidden;vertical-align:bottom;position:relative;width:var(--w,12ch);transition:width 380ms cubic-bezier(.4,0,.2,1);}
  .abc-landing .ac-track{display:flex;flex-direction:column;line-height:1.18em;transform:translateY(0);transition:transform 380ms cubic-bezier(.4,0,.2,1);}
  .abc-landing .ac-track>span{color:var(--text);font-weight:500;white-space:nowrap;}
  .abc-landing .ac-track>span.live{color:var(--accent);}
  .abc-landing .acronym-sub{display:block;margin-top:14px;color:var(--text-mute);font-size:13px;letter-spacing:.02em;}
  .abc-landing .acronym-sub i{color:var(--text-dim);font-style:normal;border-bottom:1px dotted var(--rule);padding-bottom:1px;}
  @media(prefers-reduced-motion:reduce){.abc-landing .ac-rotator,.abc-landing .ac-track{transition:none!important;{{"}}"}}
  .abc-landing .lede{font-size:15px;color:var(--text);max-width:none;margin:0 0 28px;text-wrap:pretty;}
  .abc-landing .lede code{color:var(--accent);font-weight:600;}
  .abc-landing .lede b{color:var(--ink);font-weight:700;}
  .abc-landing .install-row{display:grid;gap:12px;grid-template-columns:1fr;}
  .abc-landing .cmd{border:1px solid var(--rule);background:var(--bg-1);border-radius:4px;overflow:hidden;}
  .abc-landing .cmd-head{display:flex;align-items:center;justify-content:space-between;padding:8px 14px;border-bottom:1px solid var(--rule-soft);background:var(--bg-2);}
  .abc-landing .cmd-title{color:var(--text-dim);font-size:11px;letter-spacing:.16em;text-transform:uppercase;}
  .abc-landing .cmd-title .path{color:var(--ink);text-transform:none;letter-spacing:0;margin-left:6px;font-size:12px;}
  .abc-landing .cmd-copy{appearance:none;background:transparent;border:1px solid var(--rule);color:var(--text-dim);cursor:pointer;font-family:inherit;font-size:11px;padding:4px 10px;border-radius:3px;letter-spacing:.14em;text-transform:uppercase;transition:color .12s,border-color .12s;}
  .abc-landing .cmd-copy:hover{color:var(--ink);border-color:var(--ink-dim);}
  .abc-landing .cmd-copy.copied{color:var(--accent);border-color:var(--accent);}
  .abc-landing .cmd pre{margin:0;padding:16px;color:var(--text);font-size:13px;line-height:1.6;overflow-x:auto;font-family:inherit;}
  .abc-landing .cmd pre .prompt{color:var(--ink-dim);user-select:none;}
  .abc-landing .cmd pre .flag{color:var(--text-dim);}
  .abc-landing .cmd pre .str{color:var(--accent);}
  .abc-landing .cmd pre .cont{color:var(--text-mute);}
  .abc-landing .post-install{margin-top:18px;color:var(--text-dim);font-size:13px;}
  .abc-landing .kbd{display:inline-block;border:1px solid var(--rule);border-bottom-width:2px;background:var(--bg-1);padding:1px 6px;border-radius:3px;color:var(--ink);font-size:12px;}
  .abc-landing section.block{padding:56px 0;border-bottom:1px solid var(--rule);}
  .abc-landing section.block:last-child{border-bottom:none;}
  .abc-landing .block-head{display:grid;grid-template-columns:200px 1fr;gap:56px;margin-bottom:32px;}
  .abc-landing .block-eyebrow{color:var(--text-mute);font-size:11px;letter-spacing:.22em;text-transform:uppercase;padding-top:4px;}
  .abc-landing .block-eyebrow .num{color:var(--ink);margin-right:8px;}
  .abc-landing .block-title{color:var(--ink);font-size:22px;font-weight:600;letter-spacing:-.01em;margin:0 0 8px;}
  .abc-landing .block-desc{color:var(--text-dim);font-size:14px;max-width:64ch;margin:0;}
  .abc-landing .block-body{display:grid;grid-template-columns:200px 1fr;gap:56px;}
  .abc-landing .svc-list{border-top:1px solid var(--rule-soft);font-size:13px;}
  .abc-landing .svc-row{display:grid;grid-template-columns:22px 1fr auto auto;gap:16px;align-items:baseline;padding:10px 0;border-bottom:1px solid var(--rule-soft);color:var(--text);}
  .abc-landing .svc-row .num{color:var(--text-mute);font-size:11px;text-align:right;padding-right:4px;}
  .abc-landing .svc-row .name{color:var(--ink);font-weight:600;}
  .abc-landing .svc-row .desc{color:var(--text-dim);grid-column:2;margin-top:2px;font-size:12px;}
  .abc-landing .svc-row .url{color:var(--text-dim);font-size:12px;border:none;}
  .abc-landing .svc-row .url:hover{color:var(--accent);}
  .abc-landing .svc-row .tag{font-size:10px;letter-spacing:.14em;text-transform:uppercase;padding:2px 8px;border:1px solid var(--rule);border-radius:999px;color:var(--text-mute);}
  .abc-landing .svc-row .tag.researcher{color:var(--ink);border-color:var(--ink-dim);}
  .abc-landing .svc-row .tag.operator{color:var(--accent);border-color:var(--accent);opacity:.85;}
  .abc-landing .stack{margin-top:24px;padding:16px 18px;border:1px dashed var(--rule);border-radius:4px;color:var(--text-dim);font-size:12px;line-height:1.7;}
  .abc-landing .stack b{color:var(--ink);font-weight:600;}
  .abc-landing .stack .sep{color:var(--text-mute);margin:0 6px;}

  /* ─── Services rework ──────────────────────────────── */
  /* Sticky audience pivot */
  .abc-landing .svc-pivot{
    position:sticky;top:51px;z-index:9;
    background:linear-gradient(var(--bg) 60%, transparent);
    padding:14px 0 18px;
    margin-bottom:6px;
    display:flex;align-items:center;gap:14px;flex-wrap:wrap;
  }
  .abc-landing .svc-pivot .seg button{padding:8px 14px;font-size:11px;}
  .abc-landing .svc-pivot .count{
    color:var(--text-mute);font-size:11px;letter-spacing:.16em;text-transform:uppercase;
  }
  .abc-landing .svc-search{
    flex:1;min-width:200px;display:flex;align-items:center;gap:8px;
    border:1px solid var(--rule);border-radius:4px;
    background:var(--bg-1);padding:6px 12px;
  }
  .abc-landing .svc-search svg{width:14px;height:14px;color:var(--text-mute);flex-shrink:0;}
  .abc-landing .svc-search input{
    appearance:none;background:transparent;border:none;outline:none;
    color:var(--text);font-family:inherit;font-size:13px;
    flex:1;padding:2px 0;
  }
  .abc-landing .svc-search input::placeholder{color:var(--text-mute);}
  .abc-landing .svc-search kbd{
    color:var(--text-mute);font-size:10px;letter-spacing:.08em;
    border:1px solid var(--rule);padding:1px 5px;border-radius:3px;
    background:var(--bg-2);
  }

  /* Audience headers */
  .abc-landing .svc-section{margin-top:36px;}
  .abc-landing .svc-section:first-child{margin-top:8px;}
  .abc-landing .svc-section-head{
    display:flex;align-items:baseline;justify-content:space-between;
    margin-bottom:18px;padding-bottom:10px;
    border-bottom:1px solid var(--rule);
  }
  .abc-landing .svc-section-title{
    color:var(--ink);font-weight:600;font-size:13px;
    letter-spacing:.18em;text-transform:uppercase;
    display:flex;align-items:center;gap:10px;
  }
  .abc-landing .svc-section-title .pill{
    color:var(--text-mute);font-weight:500;letter-spacing:.14em;font-size:10px;
    border:1px solid var(--rule);padding:2px 8px;border-radius:999px;
  }
  .abc-landing .svc-section-meta{
    color:var(--text-mute);font-size:11px;letter-spacing:.14em;text-transform:uppercase;
  }

  /* Featured tile grid — large primary cards */
  .abc-landing .svc-grid{
    display:grid;grid-template-columns:repeat(3, minmax(0,1fr));gap:14px;
  }
  .abc-landing .svc-card{
    display:flex;flex-direction:column;gap:14px;
    padding:18px 20px 16px;border:1px solid var(--rule);
    background:var(--bg-1);border-radius:4px;
    color:var(--text);text-decoration:none;
    transition:border-color .14s, background .14s, transform .14s;
    position:relative;overflow:hidden;
    border-bottom:none;
  }
  .abc-landing .svc-card::after{
    content:'';position:absolute;left:0;top:0;bottom:0;width:2px;
    background:transparent;transition:background .14s;
  }
  .abc-landing .svc-card:hover{
    border-color:var(--ink-dim);background:var(--bg-2);
    transform:translateY(-1px);
  }
  .abc-landing .svc-card:hover::after{background:var(--accent);}
  .abc-landing .svc-card-head{display:flex;align-items:center;gap:10px;}
  .abc-landing .svc-card-glyph{
    width:32px;height:32px;flex-shrink:0;
    border:1px solid var(--rule);border-radius:4px;
    background:var(--bg-2);
    display:inline-flex;align-items:center;justify-content:center;
    color:var(--ink);
  }
  .abc-landing .svc-card-glyph svg{width:18px;height:18px;}
  .abc-landing .svc-card-name{
    color:var(--ink);font-weight:700;font-size:15px;letter-spacing:-.01em;
    display:flex;align-items:center;gap:8px;
  }
  .abc-landing .svc-card-name .arrow{
    color:var(--text-mute);font-weight:500;font-size:14px;
    transition:color .14s, transform .14s;
  }
  .abc-landing .svc-card:hover .arrow{color:var(--accent);transform:translateX(2px);}
  .abc-landing .svc-card-desc{color:var(--text-dim);font-size:12.5px;line-height:1.55;margin:0;flex:1;}
  .abc-landing .svc-card-foot{
    display:flex;align-items:center;justify-content:space-between;
    padding-top:12px;border-top:1px solid var(--rule-soft);
    font-size:11px;color:var(--text-mute);
  }
  .abc-landing .svc-card-host{font-family:inherit;color:var(--text-dim);letter-spacing:0;}
  .abc-landing .svc-card-status{display:inline-flex;align-items:center;gap:6px;letter-spacing:.14em;text-transform:uppercase;font-size:10px;}
  .abc-landing .status-dot{
    width:7px;height:7px;border-radius:50%;display:inline-block;flex-shrink:0;
    box-shadow:0 0 0 2px var(--bg-1);
  }
  .abc-landing .status-dot.up      {background:#4ade80;}
  .abc-landing .status-dot.degraded{background:#e8c76e;}
  .abc-landing .status-dot.down    {background:#ef4f4f;}

  /* Compact secondary list — same role for small services */
  .abc-landing .svc-mini{
    margin-top:14px;
    display:grid;grid-template-columns:repeat(2,1fr);gap:0 24px;
    border-top:1px solid var(--rule-soft);
  }
  .abc-landing .svc-mini-row{
    display:grid;grid-template-columns:auto 1fr auto;gap:10px;align-items:center;
    padding:10px 0;border-bottom:1px solid var(--rule-soft);
    color:var(--text);text-decoration:none;font-size:13px;
    border-bottom-style:solid;
  }
  .abc-landing .svc-mini-row:hover{color:var(--ink);}
  .abc-landing .svc-mini-row:hover .svc-mini-host{color:var(--accent);}
  .abc-landing .svc-mini-row .name{color:var(--ink);font-weight:600;}
  .abc-landing .svc-mini-row .desc{color:var(--text-dim);font-size:11.5px;margin-left:6px;font-weight:500;}
  .abc-landing .svc-mini-host{color:var(--text-mute);font-size:11px;letter-spacing:0;}

  .abc-landing .svc-empty{
    padding:36px 0;color:var(--text-mute);text-align:center;
    font-size:13px;letter-spacing:.06em;
  }
  .abc-landing .svc-empty b{color:var(--text-dim);font-weight:600;}

  @media(max-width:920px){
    .abc-landing .svc-grid{grid-template-columns:1fr;}
    .abc-landing .svc-mini{grid-template-columns:1fr;}
    .abc-landing .svc-pivot{position:static;}
  }
  .abc-landing .view-toggle-block{padding-bottom:32px;}
  .abc-landing .view-controls{display:flex;flex-wrap:wrap;gap:12px;align-items:center;}
  .abc-landing .view-toggle{flex-wrap:wrap;}
  .abc-landing .view-toggle-label{font-size:11px;letter-spacing:.1em;color:var(--text-mute);text-transform:uppercase;min-width:56px;}
  .abc-landing .view-toggle button{padding:9px 16px;font-size:11px;letter-spacing:.12em;}
  .abc-landing .view-toggle-sep{width:1px;height:24px;background:var(--rule);margin:0 4px;}
  .abc-landing .view-toggle-status{margin:14px 0 0;color:var(--text-mute);font-size:12px;}
  .abc-landing .view-toggle-status b{color:var(--text-dim);}
  .abc-landing footer.foot{padding:36px 0 56px;color:var(--text-mute);font-size:12px;line-height:1.7;}
  .abc-landing footer.foot .row{display:grid;grid-template-columns:200px 1fr;gap:56px;}
  .abc-landing footer.foot b{color:var(--text-dim);}
  .abc-landing footer.foot .mono-ip{color:var(--ink);}
  #tweaks-root{position:fixed;right:16px;bottom:16px;z-index:100;}
  @media(max-width:920px){
    .abc-landing .hero{padding:40px 0 32px;gap:28px;}
    .abc-landing .hero-headline{grid-template-columns:1fr;gap:24px;}
    .abc-landing .hero-cols{grid-template-columns:1fr;gap:20px;}
    .abc-landing .hero-mark{max-width:160px;margin:0 auto;}
    .abc-landing .block-head,.abc-landing .block-body,.abc-landing footer.foot .row{grid-template-columns:1fr;gap:16px;}
    .abc-landing .topbar-inner{padding:12px 18px;gap:12px;flex-wrap:wrap;}
    .abc-landing .page{padding:0 18px;}
    .abc-landing .top-meta{display:none;}
  }

  </style>
</head>
<body>

<div class="abc-landing">
<header class="topbar">
  <div class="topbar-inner">
    <a class="brand" href="/">
      <svg class="brand-mark" viewBox="0 0 100 100" aria-hidden="true">
        <line x1="20" y1="50" x2="80" y2="50" stroke="currentColor" stroke-width="3" stroke-linecap="round"/>
        <circle cx="20" cy="50" r="9" fill="var(--bg)" stroke="currentColor" stroke-width="3"/>
        <circle cx="50" cy="50" r="9" fill="var(--bg)" stroke="currentColor" stroke-width="3"/>
        <circle cx="80" cy="50" r="9" fill="var(--bg)" stroke="currentColor" stroke-width="3"/>
      </svg>
      <span class="brand-name">abc<span class="dim">-cluster</span></span>
    </a>
    <div class="top-spacer"></div>
    <a class="top-link" href="/docs/">
      <svg class="top-link-ico" viewBox="0 0 24 24" aria-hidden="true">
        <path d="M5 4h6a3 3 0 0 1 3 3v13" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
        <path d="M19 4h-6a3 3 0 0 0-3 3v13" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
        <circle cx="7" cy="8.5" r="0.9" fill="currentColor"/>
        <circle cx="7" cy="12"  r="0.9" fill="currentColor"/>
        <circle cx="7" cy="15.5" r="0.9" fill="currentColor"/>
      </svg>
      <span>Docs</span>
    </a>
    <a class="top-link" href="https://github.com/abc-cluster/abc-cluster-cli" target="_blank" rel="noreferrer">
      <svg class="top-link-ico" viewBox="0 0 24 24" aria-hidden="true">
        <path fill="currentColor" d="M12 .5a11.5 11.5 0 0 0-3.64 22.41c.58.11.79-.25.79-.56v-2c-3.2.7-3.88-1.36-3.88-1.36-.53-1.34-1.29-1.7-1.29-1.7-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.04 1.78 2.72 1.27 3.38.97.1-.76.4-1.27.74-1.56-2.55-.29-5.23-1.28-5.23-5.7 0-1.26.45-2.29 1.18-3.09-.12-.29-.51-1.46.11-3.04 0 0 .97-.31 3.18 1.18a11.04 11.04 0 0 1 5.78 0c2.21-1.49 3.18-1.18 3.18-1.18.62 1.58.23 2.75.11 3.04.74.8 1.18 1.83 1.18 3.09 0 4.43-2.69 5.41-5.25 5.69.41.36.78 1.06.78 2.13v3.16c0 .31.21.68.8.56A11.5 11.5 0 0 0 12 .5"/>
      </svg>
      <span>GitHub</span>
    </a>
    <a class="top-link" href="https://github.com/abc-cluster/abc-cluster-cli/releases" target="_blank" rel="noreferrer">
      <svg class="top-link-ico" viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 2L2 7l10 5 10-5-10-5z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/>
        <path d="M2 17l10 5 10-5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
        <path d="M2 12l10 5 10-5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <span>Releases</span>
    </a>
    <button class="theme-toggle" id="theme-toggle" aria-label="Toggle theme">
      <svg class="ti ti-sun" viewBox="0 0 28 28" aria-hidden="true">
        <circle cx="14" cy="14" r="4.2" fill="none" stroke="currentColor" stroke-width="1.6"/>
        <circle cx="14" cy="14" r="1.4" fill="currentColor"/>
        <g fill="currentColor">
          <circle cx="14" cy="3.5"  r="1.1"/><circle cx="14" cy="24.5" r="1.1"/>
          <circle cx="3.5"  cy="14" r="1.1"/><circle cx="24.5" cy="14" r="1.1"/>
          <circle cx="6.5"  cy="6.5"  r="0.9"/><circle cx="21.5" cy="6.5"  r="0.9"/>
          <circle cx="6.5"  cy="21.5" r="0.9"/><circle cx="21.5" cy="21.5" r="0.9"/>
        </g>
      </svg>
      <svg class="ti ti-moon" viewBox="0 0 28 28" aria-hidden="true">
        <path d="M19.6 17.8a8.4 8.4 0 1 1-9.4-12.6 7 7 0 0 0 9.4 12.6Z" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round" stroke-linecap="round"/>
        <circle cx="22" cy="8" r="0.8" fill="currentColor"/>
        <circle cx="6"  cy="6" r="0.7" fill="currentColor"/>
      </svg>
    </button>
  </div>
</header>

<main class="page" id="top">

  <section class="hero">
    <div class="hero-headline">
      <div class="hero-mark" aria-label="abc-cluster mark">
        <svg viewBox="0 0 200 200" aria-hidden="true">
          <defs>
            <linearGradient id="hero-brush-grad" x1="0" x2="1" y1="0" y2="0">
              <stop offset="0" stop-color="var(--brush-grad-start, var(--ink))"/>
              <stop offset="1" stop-color="var(--brush-grad-end, var(--accent))"/>
            </linearGradient>
          </defs>
          <g transform="translate(100 85)" id="hero-svg-g"></g>
          <g id="hero-wordmark">
            <text x="100" y="178" text-anchor="middle"
              style="font:600 13px 'JetBrains Mono',monospace;fill:var(--ink);letter-spacing:-0.01em;">abc-cluster</text>
            <text x="100" y="192" text-anchor="middle"
              style="font:500 6px 'JetBrains Mono',monospace;fill:var(--text-dim);letter-spacing:0.22em;">OPEN · SOVEREIGN · FEDERATED · COMPUTING</text>
          </g>
        </svg>
      </div>
      <div class="hero-headline-text">
        <div class="eyebrow">abc-cluster <span class="dot">·</span> gateway</div>
        <h1 class="acronym-h1" aria-label="African Bioinformatics Computing Cluster">
          <span class="acronym-line">
            <span class="ac ac-a"><span class="ac-letter">A</span><span class="ac-rotator"><span class="ac-track" id="ac-track"><span>frican</span><span>wesome</span><span>utomated</span><span>ffordable</span><span>daptable</span></span></span></span>
            <span class="ac"><span class="ac-letter">B</span><span class="ac-rest">ioinformatics</span></span>
            <span class="ac"><span class="ac-letter">C</span><span class="ac-rest">omputing</span></span>
            <span class="ac-rest">Cluster</span>
          </span>
          <span class="acronym-sub">a.k.a. <i>Abhi's Baby Cluster</i></span>
        </h1>
      </div>
    </div>

    <div class="hero-cols">
      <div class="hero-body">
        <p class="lede">
          The <code>abc</code> CLI is the single entrypoint to the cluster and it works on Linux, macOS, and Windows machines. <b>Researchers</b> use it to run job scripts, execute pipelines, and conduct data operations. <b>Operators</b> use it to manage the underlying cluster.
        </p>
        <div class="install-row">
          <div class="cmd">
            <div class="cmd-head">
              <div class="cmd-title">install · system-wide<span class="path">/usr/local/bin/abc</span></div>
              <button class="cmd-copy" data-copy="cmd-system">Copy</button>
            </div>
<pre id="cmd-system"><span class="prompt">$ </span>curl <span class="flag">-fsSL</span> <span class="flag">-H</span> <span class="str">"Accept: application/vnd.github.raw+json"</span> <span class="cont">\\</span>
    <span class="str">"https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main"</span> <span class="cont">\\</span>
  | sh <span class="flag">-s</span> -- <span class="flag">--sudo</span></pre>
          </div>
          <div class="cmd">
            <div class="cmd-head">
              <div class="cmd-title">install · current directory<span class="path">$PWD/abc</span></div>
              <button class="cmd-copy" data-copy="cmd-user">Copy</button>
            </div>
<pre id="cmd-user"><span class="prompt">$ </span>curl <span class="flag">-fsSL</span> <span class="flag">-H</span> <span class="str">"Accept: application/vnd.github.raw+json"</span> <span class="cont">\\</span>
    <span class="str">"https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main"</span> <span class="cont">\\</span>
  | sh <span class="flag">-s</span> --
<span class="prompt">$ </span>export PATH=<span class="str">"$PWD:$PATH"</span></pre>
          </div>
        </div>
        <p class="post-install">After install, verify with <span class="kbd">abc --version</span>.</p>
      </div>
    </div>
  </section>

  <section class="block" id="cli">
    <div class="block-head">
      <div class="block-eyebrow"><span class="num">01</span>CLI</div>
      <div>
        <h2 class="block-title">Quick-start</h2>
        <p class="block-desc">Once <code>abc</code> is on <code>$PATH</code>, set the active context and shell out to underlying CLIs through <code>abc admin services …</code>.</p>
      </div>
    </div>
    <div class="block-body">
      <div></div>
      <div class="cmd">
        <div class="cmd-head">
          <div class="cmd-title">nomad &amp; service operations</div>
          <button class="cmd-copy" data-copy="cmd-quickstart">Copy</button>
        </div>
<pre id="cmd-quickstart"><span class="prompt">$ </span>export ABC_ACTIVE_CONTEXT=abc-bootstrap
<span class="prompt">$ </span>abc admin services nomad cli -- job status
<span class="prompt">$ </span>abc admin services nomad cli -- job status abc-nodes-grafana
<span class="prompt">$ </span>abc admin services nomad cli -- job run -detach <span class="cont">\\</span>
    deployments/abc-nodes/nomad/uppy.nomad.hcl</pre>
      </div>
    </div>
  </section>

  <section class="block" id="services">
    <div class="block-head">
      <div class="block-eyebrow"><span class="num">02</span>Services</div>
      <div>
        <h2 class="block-title">Consoles &amp; endpoints</h2>
        <p class="block-desc">
          <span data-mode-domain>All consoles resolve over <code data-domain-glob>*.aither</code> via Tailscale split-DNS.</span>
          <span data-mode-subpath style="display:none">All consoles are accessible at subpaths of <code data-lan-host>aither.mb.sun.ac.za</code> — no split-DNS required.</span>
          Featured cards are the daily drivers; the compact list below covers everything else.
        </p>
      </div>
    </div>

    <div class="svc-pivot" id="svc-pivot">
      <div class="seg" data-control="audience">
        <button data-v="all" class="active">All</button>
        <button data-v="researcher">Researcher</button>
        <button data-v="operator">Operator</button>
      </div>
      <label class="svc-search">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="11" cy="11" r="6.5" fill="none" stroke="currentColor" stroke-width="1.6"/>
          <line x1="16" y1="16" x2="21" y2="21" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>
        </svg>
        <input id="svc-q" type="search" placeholder="Filter by name or description…" autocomplete="off" />
        <kbd>/</kbd>
      </label>
      <div class="count" id="svc-count"></div>
    </div>

    <div id="svc-content">
      <!-- Researcher -->
      <div class="svc-section" data-aud="researcher">
        <div class="svc-section-head">
          <div class="svc-section-title">
            Researcher
            <span class="pill">run jobs · move data</span>
          </div>
          <div class="svc-section-meta">primary</div>
        </div>

        <div class="svc-grid" data-tier="featured">
          <a class="svc-card" href="http://nomad.aither/ui/" target="_blank" rel="noreferrer"
             data-aud="researcher" data-name="nomad-ui" data-desc="job scheduler full ui allocations submit monitor">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <rect x="3" y="3" width="8" height="8" rx="1"/><rect x="13" y="3" width="8" height="8" rx="1"/>
                  <rect x="3" y="13" width="8" height="8" rx="1"/><rect x="13" y="13" width="8" height="8" rx="1"/>
                </svg>
              </span>
              <span class="svc-card-name">nomad-ui <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Job scheduler — full UI. Submit, monitor, drain, and re-run workloads; manage tokens.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">nomad.aither/ui</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://uppy.aither/" target="_blank" rel="noreferrer"
             data-aud="researcher" data-name="uppy" data-desc="browser file upload tusd resumable">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <path d="M12 16V4"/><path d="M7 9l5-5 5 5"/><path d="M4 17v3h16v-3"/>
                </svg>
              </span>
              <span class="svc-card-name">uppy <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Browser-based, resumable file upload. tusd backend; chunks straight into RustFS.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">uppy.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://rustfs.aither/rustfs/console/" target="_blank" rel="noreferrer"
             data-aud="researcher" data-name="rustfs-s3" data-desc="s3 object storage buckets files rustfs">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <ellipse cx="12" cy="6" rx="8" ry="2.5"/><path d="M4 6v6c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5V6"/>
                  <path d="M4 12v6c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5v-6"/>
                </svg>
              </span>
              <span class="svc-card-name">rustfs <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">S3 object storage — browse buckets, manage policies, share files with users.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">rustfs.aither/rustfs/console</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://ntfy.aither/" target="_blank" rel="noreferrer"
             data-aud="researcher" data-name="ntfy" data-desc="job event notifications push pub sub topic subscribe"
             data-mode-only="domain">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <path d="M18 16v-5a6 6 0 0 0-12 0v5l-2 2h16z"/><path d="M10 20a2 2 0 0 0 4 0"/>
                </svg>
              </span>
              <span class="svc-card-name">ntfy <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Job event notifications — subscribe to topics, receive push alerts when batch runs finish or fail. <em style="color:var(--text-mute);">(Tailscale only — generated links use the *.aither origin.)</em></p>
            <div class="svc-card-foot">
              <span class="svc-card-host">ntfy.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>
        </div>
      </div>

      <!-- Operator -->
      <div class="svc-section" data-aud="operator">
        <div class="svc-section-head">
          <div class="svc-section-title">
            Operator
            <span class="pill">platform &amp; control plane</span>
          </div>
          <div class="svc-section-meta">primary</div>
        </div>

        <div class="svc-grid" data-tier="featured">
          <a class="svc-card" href="http://grafana.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="grafana" data-desc="dashboards metrics logs observability">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <path d="M3 18l5-7 4 4 4-6 5 5"/><line x1="3" y1="20" x2="21" y2="20"/>
                </svg>
              </span>
              <span class="svc-card-name">grafana <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Dashboards — metrics, logs, traces. Prometheus + Loki data sources wired in.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">grafana.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://traefik.aither/dashboard/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="traefik" data-desc="load balancer service catalog routing">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <path d="M12 4v6"/><path d="M6 14l6-4 6 4"/>
                  <circle cx="6" cy="18" r="2"/><circle cx="12" cy="18" r="2"/><circle cx="18" cy="18" r="2"/>
                </svg>
              </span>
              <span class="svc-card-name">traefik <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Load balancer &amp; service catalog. Live routing table from Consul.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">traefik.aither/dashboard</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://garage-webui.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="garage" data-desc="garage long-term archive backups compression dedup s3"
             data-mode-only="domain">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <path d="M3 11l9-7 9 7"/><path d="M5 10v10h14V10"/>
                  <path d="M9 20v-6h6v6"/>
                </svg>
              </span>
              <span class="svc-card-name">garage <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Long-term archive + cluster backups. zstd compression, block-level dedup, geo-replicable. Restic repo lives here.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">garage-webui.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://gitriver.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="gitriver" data-desc="gitriver private git hosting releases artifacts container registry"
             data-mode-only="domain">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <circle cx="6" cy="6" r="2"/><circle cx="6" cy="18" r="2"/><circle cx="18" cy="12" r="2"/>
                  <path d="M6 8v8"/><path d="M8 6h6a4 4 0 0 1 4 4v0"/>
                </svg>
              </span>
              <span class="svc-card-name">gitriver <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Private git hosting — push projects, distribute releases &amp; container images, pull repos into Nomad job prestart tasks. SSH on tailnet :2222.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">gitriver.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

          <a class="svc-card" href="http://nats.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="nats" data-desc="nats jetstream messaging pubsub streams kv durable"
             data-mode-only="domain">
            <div class="svc-card-head">
              <span class="svc-card-glyph">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">
                  <circle cx="12" cy="12" r="2"/>
                  <path d="M12 2v4"/><path d="M12 18v4"/><path d="M2 12h4"/><path d="M18 12h4"/>
                  <path d="M5 5l3 3"/><path d="M16 16l3 3"/><path d="M19 5l-3 3"/><path d="M8 16l-3 3"/>
                </svg>
              </span>
              <span class="svc-card-name">nats <span class="arrow">→</span></span>
            </div>
            <p class="svc-card-desc">Pub/sub + JetStream durable streams &amp; KV for cluster events. Clients connect to nats://&lt;tailnet-ip&gt;:4222; this dashboard is the monitoring API.</p>
            <div class="svc-card-foot">
              <span class="svc-card-host">nats.aither</span>
              <span class="svc-card-status"><span class="status-dot up"></span>Up</span>
            </div>
          </a>

        </div>

        <div class="svc-mini">
          <a class="svc-mini-row" href="http://prometheus.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="prometheus" data-desc="metrics store query tsdb">
            <span class="status-dot up"></span>
            <span><span class="name">prometheus</span><span class="desc">metrics store &amp; query</span></span>
            <span class="svc-mini-host">prometheus.aither</span>
          </a>
          <a class="svc-mini-row" href="http://loki.aither/ready" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="loki" data-desc="log aggregation api">
            <span class="status-dot up"></span>
            <span><span class="name">loki</span><span class="desc">log aggregation (API)</span></span>
            <span class="svc-mini-host">loki.aither/ready</span>
          </a>
          <a class="svc-mini-row" href="http://alloy.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="alloy" data-desc="telemetry collector ui">
            <span class="status-dot up"></span>
            <span><span class="name">alloy</span><span class="desc">telemetry collector UI</span></span>
            <span class="svc-mini-host">alloy.aither</span>
          </a>
          <a class="svc-mini-row" href="http://consul.aither/ui/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="consul" data-desc="service discovery health catalog">
            <span class="status-dot up"></span>
            <span><span class="name">consul</span><span class="desc">service discovery &amp; health</span></span>
            <span class="svc-mini-host">consul.aither/ui</span>
          </a>
          <a class="svc-mini-row" href="http://vault.aither/ui/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="vault" data-desc="secrets ssh certificate authority pki">
            <span class="status-dot up"></span>
            <span><span class="name">vault</span><span class="desc">secrets &amp; SSH CA</span></span>
            <span class="svc-mini-host">vault.aither/ui</span>
          </a>
          <a class="svc-mini-row" href="http://boundary.aither/" target="_blank" rel="noreferrer"
             data-aud="operator" data-name="boundary" data-desc="ssh session broker access node">
            <span class="status-dot up"></span>
            <span><span class="name">boundary</span><span class="desc">SSH session broker</span></span>
            <span class="svc-mini-host">boundary.aither</span>
          </a>
        </div>
      </div>

      <div class="svc-empty" id="svc-empty" hidden>
        No services match <b id="svc-empty-q"></b>. Try a different term.
      </div>
    </div>
  </section>

  <!-- ── URL view toggle ─────────────────────────────────────────────────────
       Lets you preview both surfaces from one page — switch between LAN
       (subpath via aither.mb.sun.ac.za) and Tailscale (domain via *.aither)
       without having to open two landing pages.
  ── -->
  <section class="block view-toggle-block">
    <div class="block-head">
      <div class="block-eyebrow"><span class="num">99</span>Preview</div>
      <div>
        <h2 class="block-title">URL view</h2>
        <p class="block-desc">
          All cards above adapt to the network you're on. Use these toggles to preview
          links for any surface — handy for testing without opening a second browser.
        </p>
      </div>
    </div>
    <div class="view-controls">
      <span class="view-toggle-label">Network</span>
      <div class="seg view-toggle" data-control="network" role="tablist" aria-label="Network surface">
        <button data-network="auto" class="active" role="tab">Auto</button>
        <button data-network="lan" role="tab">LAN</button>
        <button data-network="tailscale" role="tab">Tailscale</button>
      </div>
      <div class="view-toggle-sep"></div>
      <span class="view-toggle-label">Format</span>
      <div class="seg view-toggle" data-control="format" role="tablist" aria-label="URL format">
        <button data-fmt="dns" class="active" role="tab">DNS</button>
        <button data-fmt="ip" role="tab">IP</button>
      </div>
    </div>
    <p class="view-toggle-status" id="view-mode-status"></p>
  </section>

  <footer class="foot">
    <div class="row">
      <div class="block-eyebrow">colophon</div>
      <div>
        <p style="margin:0 0 14px;">
          Traffic enters via <b data-lan-host>aither.mb.sun.ac.za</b> (LAN) and is proxied by Caddy
          through Traefik to backend services on private / Tailscale addresses.
          Service vhosts are at <code data-domain-glob>*.aither</code> — add
          <span class="mono-ip" data-ts-ip>100.70.185.46</span> as a split-DNS nameserver for
          <code data-domain-suffix>.aither</code> in the Tailscale admin console to resolve them on any
          tailnet device. <a href="/docs/">abc CLI docs →</a>
        </p>
        <div style="color:var(--text-mute);font-size:11.5px;line-height:1.7;border-top:1px solid var(--rule-soft);padding-top:12px;">
          <b style="color:var(--text-dim);">Stack</b>
          <span class="sep">·</span> Tailscale (overlay)
          <span class="sep">→</span> Caddy (vhost / TLS)
          <span class="sep">→</span> Traefik (Consul-catalog LB)
          <span class="sep">→</span> Nomad services
          <span class="sep">·</span> discovery via <b style="color:var(--text-dim);">Consul</b>
          <span class="sep">·</span> secrets &amp; SSH CA via <b style="color:var(--text-dim);">Vault</b>
          <span class="sep">·</span> SSH sessions via <b style="color:var(--text-dim);">Boundary</b>
        </div>
      </div>
    </div>
  </footer>

</main>
<div id="tweaks-root"></div>
</div>

<script>

(function() {
  /* ── Single-source network config ─────────────────────────────────────────
     Change these three values once and all service links, host labels,
     and footer references update automatically.
     LAN deploy  : domain='aither', lanHost='aither.mb.sun.ac.za', tsIP='146.232.174.77'
     Tailscale   : domain='aither', lanHost='aither.mb.sun.ac.za', tsIP='100.70.185.46'
  ── */
  // Auto-detect network surface and URL format from the hostname this page was served on.
  //   tailscale + dns : *.${var.service_domain}   e.g. grafana.aither
  //   tailscale + ip  : ${var.ts_ip}             bare Tailscale IP
  //   lan + dns       : ${var.lan_host}           institutional hostname
  //   lan + ip        : ${var.lan_ip}             bare LAN IP
  function detectState() {
    var h = window.location.hostname;
    if (/\.${var.service_domain}$/.test(h)) return { network: 'tailscale', format: 'dns' };
    if (h === '${var.ts_ip}')               return { network: 'tailscale', format: 'ip'  };
    if (h === '${var.lan_ip}')              return { network: 'lan',       format: 'ip'  };
    return                                         { network: 'lan',       format: 'dns' };
  }

  var DETECTED = detectState();

  var CONFIG = {
    network: DETECTED.network,        // 'tailscale' | 'lan'
    format:  DETECTED.format,         // 'dns' | 'ip'
    viewNetwork: 'auto',              // toggle state: 'auto' | 'lan' | 'tailscale'
    domain:  '${var.service_domain}',
    lanHost: '${var.lan_host}',
    lanIP:   '${var.lan_ip}',
    tsIP:    '${var.ts_ip}',
  };

  // ── URL computation helpers ───────────────────────────────────────────────
  // Returns the effective host string for subpath-mode URLs given current config.
  function subpathHost() {
    if (CONFIG.network === 'tailscale') return CONFIG.tsIP;       // 100.70.185.46
    return CONFIG.format === 'ip' ? CONFIG.lanIP : CONFIG.lanHost; // 146.x or hostname
  }

  // True when Tailscale domain-vhost URLs (*.aither) should be used.
  function isDomainMode() {
    return CONFIG.network === 'tailscale' && CONFIG.format === 'dns';
  }

  // Rewrite a canonical *.aither href into subpath form for the given host.
  // Services whose path ALREADY contains the svc segment (nomad, docs, rustfs)
  // are kept as-is to avoid doubling the prefix (e.g. /rustfs/rustfs/console/).
  function toSubpathHref(origHref, host) {
    return origHref.replace(/^http:\/\/([^./]+)\.aither(\/[^]*)?$/, function(_, svc, path) {
      if (svc === 'nomad' || svc === 'docs' || svc === 'rustfs') {
        return 'http://' + host + (path || '/');
      }
      return 'http://' + host + '/' + svc + (path || '/');
    });
  }

  // Rewrite a canonical *.aither host label into subpath label.
  function toSubpathLabel(origText, host) {
    return origText.replace(/^([^./]+)\.aither(\/[^]*)?$/, function(_, svc, path) {
      if (svc === 'nomad')  return host + (path || '/ui');
      if (svc === 'docs')   return host + (path || '/docs');
      if (svc === 'rustfs') return host + (path || '/');
      return host + '/' + svc + (path || '');
    });
  }

  function applyConfig() {
    var d    = CONFIG.domain;
    var dom  = isDomainMode();
    var host = subpathHost();

    // ── Rewrite service link hrefs ────────────────────────────────────────
    document.querySelectorAll('a[href]').forEach(function(a) {
      if (!a.dataset.origHref) {
        var cur = a.getAttribute('href');
        if (!cur || cur.indexOf('.aither') < 0) return;
        a.dataset.origHref = cur;
      }
      var h = dom
        ? a.dataset.origHref.replace(/\.aither/g, '.' + d)
        : toSubpathHref(a.dataset.origHref, host);
      a.setAttribute('href', h);
    });

    // ── Rewrite host labels in service cards ──────────────────────────────
    document.querySelectorAll('.svc-card-host, .svc-mini-host').forEach(function(el) {
      if (!el.dataset.origText) el.dataset.origText = el.textContent;
      var t = dom
        ? el.dataset.origText.replace(/\.aither/g, '.' + d)
        : toSubpathLabel(el.dataset.origText, host);
      el.textContent = t;
    });

    // ── Footer / description data-tagged nodes ────────────────────────────
    document.querySelectorAll('[data-lan-host]').forEach(function(el){ el.textContent = CONFIG.lanHost; });
    document.querySelectorAll('[data-ts-ip]').forEach(function(el){ el.textContent = CONFIG.tsIP; });
    if (dom) {
      document.querySelectorAll('[data-domain-glob]').forEach(function(el){ el.textContent = '*.' + d; });
      document.querySelectorAll('[data-domain-suffix]').forEach(function(el){ el.textContent = '.' + d; });
    } else {
      document.querySelectorAll('[data-domain-glob]').forEach(function(el){ el.textContent = host + '/<service>'; });
      document.querySelectorAll('[data-domain-suffix]').forEach(function(el){ el.textContent = '/<service>'; });
    }

    // ── Show/hide mode-specific content blocks ────────────────────────────
    document.querySelectorAll('[data-mode-subpath]').forEach(function(el){
      el.style.display = dom ? 'none' : '';
    });
    document.querySelectorAll('[data-mode-domain]').forEach(function(el){
      el.style.display = dom ? '' : 'none';
    });

    // ── Hide network-specific service cards in the opposite network ───────
    // data-mode-only="domain" = Tailscale only; data-mode-only="subpath" = LAN only.
    var netMode = (CONFIG.network === 'tailscale') ? 'domain' : 'subpath';
    document.querySelectorAll('[data-mode-only]').forEach(function(el){
      el.style.display = (el.dataset.modeOnly === netMode) ? '' : 'none';
    });

    // ── Update active button states ───────────────────────────────────────
    document.querySelectorAll('[data-control="network"] button').forEach(function(b){
      b.classList.toggle('active', b.dataset.network === CONFIG.viewNetwork);
    });
    document.querySelectorAll('[data-control="format"] button').forEach(function(b){
      b.classList.toggle('active', b.dataset.fmt === CONFIG.format);
    });

    // ── Update status line ────────────────────────────────────────────────
    var statusEl = document.getElementById('view-mode-status');
    if (statusEl) {
      var netLabel  = CONFIG.network === 'tailscale' ? 'Tailscale' : 'LAN';
      var fmtLabel  = dom ? '*.' + d : host + '/<service>';
      var autoNote  = CONFIG.viewNetwork === 'auto'
        ? ' (auto-detected from this hostname)'
        : '';
      statusEl.innerHTML = 'Showing <b>' + netLabel + ' — ' + fmtLabel + '</b>' + autoNote + '.';
    }
  }

  function setNetwork(net) {
    CONFIG.viewNetwork = net;
    CONFIG.network = (net === 'auto') ? DETECTED.network : net;
    // When switching networks, preserve format unless format is incompatible.
    applyConfig();
  }

  function setFormat(fmt) {
    CONFIG.format = fmt;
    applyConfig();
  }

  var TWEAK_DEFAULTS = { theme: 'dark', system: 'grid' };

  var AFRICA_PATH =
    'M -18 -78 L 18 -76 L 34 -64 L 40 -48 L 48 -30 L 60 -18 L 52 -6 L 46 14 ' +
    'L 40 36 L 28 58 L 10 74 L -6 78 L -22 72 L -30 52 L -28 32 L -20 12 ' +
    'L -14 2 L -32 -2 L -50 -4 L -58 -18 L -54 -36 L -40 -52 L -32 -68 Z';
  var AFRICA_POLY = [[-18,-78],[18,-76],[34,-64],[40,-48],[48,-30],[60,-18],[52,-6],[46,14],[40,36],[28,58],[10,74],[-6,78],[-22,72],[-30,52],[-28,32],[-20,12],[-14,2],[-32,-2],[-50,-4],[-58,-18],[-54,-36],[-40,-52],[-32,-68]];

  function isInsideAfrica(px,py){
    var inside=false;
    for(var i=0,j=AFRICA_POLY.length-1;i<AFRICA_POLY.length;j=i++){
      var xi=AFRICA_POLY[i][0],yi=AFRICA_POLY[i][1],xj=AFRICA_POLY[j][0],yj=AFRICA_POLY[j][1];
      var intersect=((yi>py)!==(yj>py))&&(px<(xj-xi)*(py-yi)/(yj-yi)+xi);
      if(intersect)inside=!inside;
    }
    return inside;
  }

  var NS='http://www.w3.org/2000/svg';
  function svgEl(tag,attrs){var n=document.createElementNS(NS,tag);for(var k in attrs)n.setAttribute(k,attrs[k]);return n;}

  function drawHeroMark(){
    var g=document.getElementById('hero-svg-g');
    if(!g)return;
    g.innerHTML='';
    var sys=document.documentElement.dataset.system;
    if(sys==='brush'){
      g.appendChild(svgEl('path',{d:AFRICA_PATH,fill:'none',stroke:'url(#hero-brush-grad)','stroke-width':'4.2','stroke-linejoin':'round','stroke-linecap':'round'}));
      [['A',-32,98],['B',0,98],['C',32,98]].forEach(function(v){
        g.appendChild(svgEl('circle',{cx:v[1],cy:v[2],r:9,fill:'var(--bg)',stroke:'var(--ink)','stroke-width':1.6}));
        var t=svgEl('text',{x:v[1],y:v[2]+3.5,'text-anchor':'middle','font-family':'JetBrains Mono','font-size':'9','font-weight':'700','fill':'var(--ink)'});
        t.textContent=v[0];g.appendChild(t);
      });
    } else {
      g.appendChild(svgEl('path',{d:AFRICA_PATH,fill:'none',stroke:'var(--ink-dim)','stroke-width':'0.8',opacity:'0.5'}));
      for(var gy=-78;gy<=78;gy+=6)for(var gx=-60;gx<=60;gx+=6)
        if(isInsideAfrica(gx,gy))g.appendChild(svgEl('circle',{cx:gx,cy:gy,r:1.3,fill:'var(--ink)',opacity:'0.7'}));
      [['A',-32,20],['B',0,30],['C',32,20]].forEach(function(v){
        g.appendChild(svgEl('circle',{cx:v[1],cy:v[2],r:9,fill:'var(--bg)',stroke:'var(--accent)','stroke-width':1.4}));
        var t=svgEl('text',{x:v[1],y:v[2]+3,'text-anchor':'middle','font-family':'JetBrains Mono','font-size':'9','font-weight':'700','fill':'var(--accent)'});
        t.textContent=v[0];g.appendChild(t);
      });
    }
  }

  function setTheme(t){document.documentElement.dataset.theme=t;}
  function setSystem(s){document.documentElement.dataset.system=s;drawHeroMark();}

  setTheme(TWEAK_DEFAULTS.theme);
  setSystem(TWEAK_DEFAULTS.system);
  applyConfig();

  var themeBtn=document.getElementById('theme-toggle');
  if(themeBtn)themeBtn.addEventListener('click',function(){
    var next=document.documentElement.dataset.theme==='dark'?'light':'dark';
    setTheme(next);
  });

  // Network toggle (Auto / LAN / Tailscale).
  document.querySelectorAll('[data-control="network"] button').forEach(function(btn){
    btn.addEventListener('click', function(){ setNetwork(btn.dataset.network); });
  });
  // Format toggle (DNS / IP).
  document.querySelectorAll('[data-control="format"] button').forEach(function(btn){
    btn.addEventListener('click', function(){ setFormat(btn.dataset.fmt); });
  });
  // Initial render — paint status line, sync active buttons.
  applyConfig();

  (function rotateAcronym(){
    var track=document.getElementById('ac-track');
    if(!track)return;
    var rotator=track.parentElement;
    var words=Array.from(track.children);
    if(!words.length)return;
    var lineH=parseFloat(getComputedStyle(track).lineHeight)||parseFloat(getComputedStyle(track).fontSize)*1.18;
    var i=0;
    function measure(){return words.map(function(w){return Math.ceil(w.getBoundingClientRect().width)+2;});}
    function remeasure(){
      var widths=measure();
      var cur=(i-1+words.length)%words.length;
      rotator.style.setProperty('--w',widths[cur]+'px');
    }
    function tick(){
      var widths=measure();
      words.forEach(function(w,n){w.classList.toggle('live',n===i);});
      rotator.style.setProperty('--w',widths[i]+'px');
      track.style.transform='translateY('+(-i*lineH)+'px)';
      i=(i+1)%words.length;
    }
    tick();
    setInterval(tick,2200);
    if(document.fonts&&document.fonts.ready){document.fonts.ready.then(remeasure);}
  })();

  async function copyText(txt){
    if(navigator.clipboard&&navigator.clipboard.writeText){
      try{await navigator.clipboard.writeText(txt);return;}catch(e){}
    }
    var ta=document.createElement('textarea');
    ta.value=txt;ta.style.cssText='position:fixed;top:0;left:0;opacity:0;pointer-events:none;';
    document.body.appendChild(ta);ta.focus();ta.select();
    try{document.execCommand('copy');}finally{document.body.removeChild(ta);}
  }
  /* ─── Services pivot + filter ─────────────────────────────────────── */
  (function services(){
    var pivot=document.getElementById('svc-pivot');
    if(!pivot)return;
    var seg=pivot.querySelector('[data-control="audience"]');
    var input=document.getElementById('svc-q');
    var countEl=document.getElementById('svc-count');
    var emptyEl=document.getElementById('svc-empty');
    var emptyQ=document.getElementById('svc-empty-q');
    var sections=Array.prototype.slice.call(document.querySelectorAll('#svc-content .svc-section'));
    var items=Array.prototype.slice.call(document.querySelectorAll('#svc-content [data-name]'));

    var audience='all';
    var query='';

    function apply(){
      var q=query.trim().toLowerCase();
      var visible=0;
      items.forEach(function(el){
        var aud=el.getAttribute('data-aud');
        var name=(el.getAttribute('data-name')||'').toLowerCase();
        var desc=(el.getAttribute('data-desc')||'').toLowerCase();
        var audOk=(audience==='all'||aud===audience);
        var qOk=(!q||name.indexOf(q)>=0||desc.indexOf(q)>=0);
        var on=audOk&&qOk;
        el.style.display=on?'':'none';
        if(on)visible++;
      });
      sections.forEach(function(sec){
        var aud=sec.getAttribute('data-aud');
        var audOk=(audience==='all'||aud===audience);
        var anyVisible=Array.prototype.some.call(sec.querySelectorAll('[data-name]'),function(el){return el.style.display!=='none';});
        sec.style.display=(audOk&&anyVisible)?'':'none';
      });
      if(emptyEl){
        emptyEl.hidden=visible>0;
        if(emptyQ)emptyQ.textContent=q?('“'+q+'”'):'your filter';
      }
      if(countEl){
        countEl.textContent=visible+(visible===1?' service':' services');
      }
    }

    seg.addEventListener('click',function(e){
      var b=e.target.closest('button[data-v]');
      if(!b)return;
      Array.prototype.forEach.call(seg.querySelectorAll('button'),function(x){x.classList.remove('active');});
      b.classList.add('active');
      audience=b.getAttribute('data-v');
      apply();
    });
    if(input){
      input.addEventListener('input',function(){query=input.value;apply();});
    }
    document.addEventListener('keydown',function(e){
      if(e.key==='/'&&document.activeElement!==input&&!e.metaKey&&!e.ctrlKey){
        var t=e.target;var tag=t&&t.tagName;
        if(tag==='INPUT'||tag==='TEXTAREA'||(t&&t.isContentEditable))return;
        e.preventDefault();
        if(input)input.focus();
      } else if(e.key==='Escape'&&document.activeElement===input){
        input.value='';query='';apply();input.blur();
      }
    });
    apply();
  })();

  document.querySelectorAll('.cmd-copy').forEach(function(btn){
    btn.addEventListener('click',async function(){
      var pre=document.getElementById(btn.dataset.copy);
      if(!pre)return;
      await copyText(pre.innerText.replace(/^\\$ /gm,''));
      var orig=btn.textContent;
      btn.textContent='Copied';btn.classList.add('copied');
      setTimeout(function(){btn.textContent=orig;btn.classList.remove('copied');},1400);
    });
  });
})();

</script>
</body>
</html>
LANDING_HTML
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
