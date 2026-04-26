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
{
  auto_https disable_redirects
  # Port 2020: avoids conflict with system Caddy which owns 127.0.0.1:2019.
  admin 127.0.0.1:2020
}

# Tailscale interface only — system Caddy owns the LAN IP (146.232.174.77).
# *.aither DNS resolves to 100.70.185.46 via dnsmasq (/etc/dnsmasq.d/20-aither.conf).
(ts_bind) {
  bind 100.70.185.46
}

# ══ SERVICE VHOSTS (*.aither) ══════════════════════════════════════════════════
# Routing: Caddy (Host match) → Traefik:8081 (Consul-catalog LB) → live backend.
# Traefik reads the backend from Consul on every request — follows rescheduled
# allocations automatically, load-balances across multiple instances.

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

http://minio.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://minio-console.aither {
  import ts_bind
  reverse_proxy abc-nodes-traefik.service.consul:8081
}

http://rustfs.aither {
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

# tusd — ForwardAuth via auth service; preflight and Uppy-Referer requests bypass.
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

# Direct proxies — bypass Traefik (single-node services or bootstrapping concerns).
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

# ══ LANDING PAGE + INSTITUTIONAL HOST ═════════════════════════════════════════
# Matches both the hostname and bare Tailscale IP (Host: 100.70.185.46).

http://aither.mb.sun.ac.za,
http://100.70.185.46 {
  import ts_bind

  @root path /
  handle @root {
    root * {{ env "NOMAD_TASK_DIR" }}/landing
    rewrite * /index.html
    file_server
  }

  # Nomad UI/API — SPA rootURL is hardcoded to /ui/
  handle /ui/* {
    reverse_proxy 100.70.185.46:4646
  }
  handle /v1/* {
    reverse_proxy 100.70.185.46:4646
  }

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
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>abc-cluster gateway</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; }
    body {
      font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      margin: 0;
      background:
        radial-gradient(circle at 20% 0%, #0c2520 0, #06130f 52%),
        #060f0c;
      color: #e8f5f1;
      min-height: 100vh;
      padding: 1.25rem;
    }
    .shell { width: 100%; max-width: 1040px; margin: 0 auto; }
    .hero {
      border: 1px solid #174d42;
      background: linear-gradient(145deg, #0b201b, #0b1714);
      border-radius: 16px;
      padding: 1.35rem;
      margin-bottom: 1rem;
      box-shadow: 0 12px 36px rgba(0,0,0,0.25);
    }
    .chip {
      display: inline-block;
      background: #0b241f;
      border: 1px solid #174d42;
      border-radius: 999px;
      font-size: 0.72rem;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      padding: 0.35rem 0.8rem;
      color: #4cb09d;
      margin-bottom: 0.8rem;
    }
    h1 {
      margin: 0;
      font-size: clamp(1.4rem, 2.8vw, 2.05rem);
      font-weight: 700;
      letter-spacing: -0.02em;
      color: #f2fffc;
    }
    .sub { margin: 0.6rem 0 0; color: #b9dfd6; line-height: 1.55; max-width: 75ch; }
    h2 {
      margin: 0 0 0.7rem;
      font-size: 0.82rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: #7eb7ab;
    }
    .meta, .cta-row {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 0.65rem;
      margin-top: 1rem;
    }
    .meta-item {
      border: 1px solid #16443b;
      background: #0b1d18;
      border-radius: 10px;
      padding: 0.65rem 0.75rem;
      color: #c3e7df;
      font-size: 0.9rem;
    }
    .cta {
      text-decoration: none;
      border: 1px solid #1b5d4f;
      background: #0b231d;
      color: #dcfff7;
      border-radius: 10px;
      padding: 0.68rem 0.78rem;
      font-weight: 600;
      transition: background 0.15s ease, border-color 0.15s ease;
    }
    .cta:hover { background: #113028; border-color: #2a7e6c; }
    .section {
      border: 1px solid #133d34;
      background: #091713;
      border-radius: 14px;
      padding: 1rem;
      margin-bottom: 1rem;
    }
    .install {
      margin-top: 0.8rem;
      border: 1px solid #174d42;
      border-radius: 12px;
      background: #081712;
      padding: 0.9rem;
    }
    .install h3 { margin: 0 0 0.55rem; font-size: 0.9rem; color: #9fd6cb; }
    pre {
      margin: 0;
      overflow-x: auto;
      color: #d5fff5;
      font-size: 0.84rem;
      line-height: 1.45;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; color: #69b7a8; }
    .hint { margin-top: 0.8rem; color: #9ccfc4; font-size: 0.86rem; line-height: 1.4; }
    .hint:first-child { margin-top: 0; }
    .ops {
      border: 1px solid #123931;
      background: #081411;
      border-radius: 14px;
      padding: 1rem;
      margin-bottom: 1rem;
    }
    .ops-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 0.5rem; }
    .op-link {
      display: flex;
      flex-direction: column;
      text-decoration: none;
      border: 1px solid #17493f;
      background: #0b1d18;
      color: #d8f7f0;
      border-radius: 10px;
      padding: 0.62rem 0.72rem;
      font-size: 0.86rem;
      transition: border-color 0.15s ease, background 0.15s ease;
    }
    .op-link:hover { border-color: #2a7e6c; background: #0f2a23; }
    .op-link .label { font-weight: 600; margin-bottom: 0.15rem; }
    .op-link .desc { font-size: 0.76rem; color: #6db09f; }
    .stack-note { margin-top: 0.75rem; font-size: 0.78rem; color: #5d9e8e; line-height: 1.4; }
    .footer { margin-top: 1rem; color: #8bbeb2; font-size: 0.78rem; line-height: 1.4; }
    @media (max-width: 720px) {
      body { padding: 0.85rem; }
      .hero, .section, .ops { padding: 0.85rem; }
      .chip { font-size: 0.68rem; }
      .cta { font-size: 0.9rem; }
      pre { font-size: 0.78rem; }
    }
  </style>
</head>
<body>
  <main class="shell">

    <!-- Hero -->
    <section class="hero">
      <div class="chip">open source cluster tooling</div>
      <h1>ABC Cluster Gateway</h1>
      <p class="sub">Run pipelines, jobs, data transfer, and cluster automation from one command-line workflow. Install the <span class="mono">abc</span> CLI to get started; service consoles are linked below for operators and researchers.</p>
      <div class="meta">
        <div class="meta-item"><strong>Base URL</strong><br><a class="mono" href="http://100.70.185.46" style="color:#69b7a8;text-decoration:none;">http://100.70.185.46</a></div>
        <div class="meta-item"><strong>Primary tool</strong><br><span class="mono">abc</span> CLI</div>
        <div class="meta-item"><strong>Audience</strong><br>Researchers &amp; operators</div>
      </div>
      <div class="cta-row">
        <a class="cta" href="https://github.com/abc-cluster/abc-cluster-cli" target="_blank" rel="noreferrer">View CLI repository</a>
        <a class="cta" href="https://github.com/abc-cluster/abc-cluster-cli/releases" target="_blank" rel="noreferrer">Download releases</a>
      </div>
    </section>

    <!-- Install -->
    <section class="section">
      <h2>Install abc CLI</h2>
      <p class="hint">Use the official installer to fetch the correct binary for your platform (Linux / macOS / Windows).</p>
      <div class="install">
        <h3>System-wide install (<span class="mono">/usr/local/bin/abc</span>)</h3>
        <pre>curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \
  | sh -s -- --sudo</pre>
      </div>
      <div class="install">
        <h3>User-local install (<span class="mono">~/bin/abc</span>, no sudo)</h3>
        <pre>curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \
  | sh -s --</pre>
      </div>
      <p class="hint">After install: <span class="mono">abc auth login</span> &#x2192; <span class="mono">abc --help</span></p>
    </section>

    <!-- Researcher apps -->
    <section class="ops">
      <h2>Researcher applications</h2>
      <div class="ops-grid">
        <a class="op-link" href="http://100.70.185.46:4646/ui/settings/tokens" target="_blank" rel="noreferrer">
          <span class="label">Nomad portal</span>
          <span class="desc">Job scheduler UI &amp; token management · :4646</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8090/" target="_blank" rel="noreferrer">
          <span class="label">Uppy uploads</span>
          <span class="desc">Browser-based file upload (tusd backend) · :8090</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:9001/" target="_blank" rel="noreferrer">
          <span class="label">MinIO Console</span>
          <span class="desc">S3 object storage — buckets &amp; files · :9001</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8088/" target="_blank" rel="noreferrer">
          <span class="label">ntfy</span>
          <span class="desc">Job event notifications · :8088</span>
        </a>
      </div>
      <p class="hint">All links use the Tailscale IP directly — no DNS configuration required.</p>
    </section>

    <!-- Operator consoles -->
    <section class="ops">
      <h2>Platform operators</h2>
      <div class="ops-grid">
        <a class="op-link" href="http://100.70.185.46:3000/" target="_blank" rel="noreferrer">
          <span class="label">Grafana</span>
          <span class="desc">Dashboards — metrics &amp; logs · :3000</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:9090/" target="_blank" rel="noreferrer">
          <span class="label">Prometheus</span>
          <span class="desc">Metrics store &amp; query · :9090</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:3100/ready" target="_blank" rel="noreferrer">
          <span class="label">Loki</span>
          <span class="desc">Log aggregation (API) · :3100</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:12345/" target="_blank" rel="noreferrer">
          <span class="label">Grafana Alloy</span>
          <span class="desc">Telemetry collector UI · :12345</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8888/dashboard/" target="_blank" rel="noreferrer">
          <span class="label">Traefik</span>
          <span class="desc">Load balancer &amp; service catalog · :8888</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8500/ui/" target="_blank" rel="noreferrer">
          <span class="label">Consul</span>
          <span class="desc">Service discovery &amp; health · :8500</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:4646/ui/" target="_blank" rel="noreferrer">
          <span class="label">Nomad UI</span>
          <span class="desc">Job scheduler — full view · :4646</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:9000/" target="_blank" rel="noreferrer">
          <span class="label">MinIO S3 API</span>
          <span class="desc">S3-compatible endpoint (mc / SDK) · :9000</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:9901/" target="_blank" rel="noreferrer">
          <span class="label">RustFS Console</span>
          <span class="desc">Alternate S3-compatible store · :9901</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8200/ui/" target="_blank" rel="noreferrer">
          <span class="label">Vault</span>
          <span class="desc">Secrets &amp; SSH certificate authority · :8200</span>
        </a>
        <a class="op-link" href="http://100.70.185.46:8080/" target="_blank" rel="noreferrer">
          <span class="label">tusd</span>
          <span class="desc">Upload endpoint (tus protocol) · :8080</span>
        </a>
      </div>
      <div class="stack-note">
        Stack: <strong>Tailscale</strong> (overlay network) &#x2192;
        <strong>Nomad</strong> services (direct IP:port) &middot;
        Load balancing via <strong>Traefik</strong> · Service discovery via <strong>Consul</strong> &middot;
        Secrets &amp; SSH CA via <strong>Vault</strong>
      </div>
    </section>

    <!-- CLI quick-start -->
    <section class="section">
      <h2>CLI quick-start (abc on PATH)</h2>
      <div class="install">
        <h3>Nomad and service operations</h3>
        <pre>export ABC_ACTIVE_CONTEXT=abc-bootstrap
abc admin services nomad cli -- job status
abc admin services nomad cli -- job status abc-nodes-grafana
abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/uppy.nomad.hcl</pre>
      </div>
    </section>

    <p class="footer">
      Services are accessible directly via Tailscale IP <span class="mono">100.70.185.46</span> on their native ports — no DNS setup required.
      LAN access via <strong>aither.mb.sun.ac.za</strong> is served by the system Caddy on the institutional network.
    </p>

  </main>
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
