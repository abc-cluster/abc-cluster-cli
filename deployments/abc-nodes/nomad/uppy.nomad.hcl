# Uppy file-upload dashboard — abc-nodes floor
# Serves a static Uppy Dashboard page backed by the existing tusd TUS server.
# LAN mode  (Tailscale off): tusd_endpoint = http://aither.mb.sun.ac.za/files/
# Tailscale mode:            tusd_endpoint = http://tusd.aither/files/

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "nginx_image" {
  type    = string
  default = "nginx:1.27-alpine"
}

variable "tusd_endpoint" {
  type        = string
  description = "TUS upload endpoint (browser-accessible URL, must be reachable from the user's browser)."
  # LAN (no split-DNS): http://aither.mb.sun.ac.za/files/
  # Tailscale:          http://tusd.aither/files/
  default     = "http://aither.mb.sun.ac.za/files/"
}

variable "uppy_max_file_size_mb" {
  type    = number
  default = 5120
}

job "abc-nodes-uppy" {
  namespace = "abc-applications"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "uppy"
  }

  group "uppy" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 8090
        to     = 8090
      }
    }

    task "uppy" {
      driver = "containerd-driver"

      config {
        image = var.nginx_image
        args  = ["nginx", "-g", "daemon off;", "-c", "/local/nginx.conf"]
      }

      template {
        destination = "local/nginx.conf"
        data        = <<EOF
events {}
http {
  include      /etc/nginx/mime.types;
  default_type application/octet-stream;
  server {
    listen 8090;
    root  /local/html;
    index index.html;
    location / {
      try_files $uri $uri/ /index.html =404;
    }
  }
}
EOF
      }

      template {
        destination = "local/html/index.html"
        data        = <<EOF
<!DOCTYPE html>
<html data-theme="dark" lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Upload · abc-cluster</title>
  <meta name="description" content="Resumable file uploads to the abc-cluster object store.">
  <!-- Favicon: africa-dot-grid brand kit, dark variant (uppy renders on a dark
       surface). Encoded inline so the page is self-contained and survives any
       Caddy/Traefik path rewriting. Source: docs/assets/africa-dot-grid/favicon-dark.svg -->
  <link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20viewBox='0%200%2064%2064'%20role='img'%20aria-label='abc-cluster%20A'%3E%3Crect%20width='64'%20height='64'%20rx='11.52'%20fill='%23070f0c'/%3E%3Ccircle%20cx='32'%20cy='32'%20r='25.6'%20fill='none'%20stroke='%23c8a84c'%20stroke-width='3.84'/%3E%3Ctext%20x='32'%20y='45.4912'%20text-anchor='middle'%20font-family='JetBrains%20Mono,ui-monospace,Menlo,Consolas,monospace'%20font-weight='700'%20font-size='39.68'%20fill='%23c8a84c'%20letter-spacing='-0.02em'%3EA%3C/text%3E%3C/svg%3E">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet">
  <link rel="stylesheet" href="https://releases.transloadit.com/uppy/v4.13.3/uppy.min.css">
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

    :root {
      --bg:        #070f0c;
      --bg-1:      #0b1a15;
      --bg-2:      #0f2219;
      --text:      #c5e5dc;
      --text-dim:  #7db8a8;
      --text-mute: #4a7d6f;
      --ink:       #e8f9f4;
      --ink-dim:   #c8a84c;
      --accent:    #c8a84c;
      --primary:   #4ab89a;
      --rule:      #163d32;
      --rule-soft: #0e2a22;
      --selection: rgba(200,168,76,0.22);
    }

    html, body {
      min-height: 100vh;
      background: var(--bg);
      color: var(--text);
      font-family: 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 14px;
      line-height: 1.55;
      font-feature-settings: 'ss02','ss03';
      -webkit-font-smoothing: antialiased;
    }
    ::selection { background: var(--selection); color: var(--ink); }

    /* ── Topbar ──────────────────────────────────────────── */
    .topbar {
      border-bottom: 1px solid var(--rule);
      background: var(--bg);
      position: sticky; top: 0; z-index: 10;
    }
    .topbar-inner {
      max-width: 1180px; margin: 0 auto;
      padding: 14px 32px;
      display: flex; align-items: center; gap: 12px;
    }
    .brand {
      display: flex; align-items: center; gap: 10px;
      font-weight: 600; font-size: 15px;
      color: var(--ink); text-decoration: none;
      letter-spacing: -0.01em;
    }
    .brand-mark { width: 22px; height: 22px; color: var(--ink); }
    .brand-dim  { color: var(--text-dim); }
    .top-spacer { flex: 1; }
    .top-link {
      display: inline-flex; align-items: center; gap: 8px;
      padding: 7px 12px; border-radius: 4px;
      border: 1px solid var(--rule); background: var(--bg-1);
      color: var(--text); font-size: 13px; font-weight: 500;
      font-family: inherit; letter-spacing: -0.01em;
      text-decoration: none;
      transition: color .12s, border-color .12s, background .12s;
    }
    .top-link:hover { color: var(--ink); border-color: var(--ink-dim); background: var(--bg-2); }

    /* ── Main layout ─────────────────────────────────────── */
    main {
      max-width: 960px; margin: 0 auto;
      padding: 56px 32px 80px;
    }

    .eyebrow {
      font-size: 11px; letter-spacing: 0.14em;
      text-transform: uppercase; color: var(--text-mute);
      margin-bottom: 12px;
      display: flex; align-items: center; gap: 8px;
    }
    .eyebrow .num { color: var(--accent); font-weight: 600; }

    h1 {
      font-size: clamp(1.4rem, 3vw, 1.9rem);
      font-weight: 600; letter-spacing: -0.02em;
      color: var(--ink); margin-bottom: 8px;
    }
    h1 .dim { color: var(--text-dim); }

    .subtitle {
      font-size: 13px; color: var(--text-dim);
      margin-bottom: 32px; max-width: 620px; line-height: 1.6;
    }

    .info-bar {
      background: var(--bg-1); border: 1px solid var(--rule);
      border-radius: 4px; padding: 10px 16px;
      font-size: 12.5px; color: var(--text-dim);
      margin-bottom: 28px; line-height: 1.6;
    }
    .info-bar code {
      background: var(--bg-2); border: 1px solid var(--rule);
      border-radius: 3px; padding: 1px 5px;
      font-family: inherit; font-size: 12px; color: var(--text);
    }

    .uppy-wrap { display: flex; justify-content: center; }

    /* ── Uppy widget overrides (abc-cluster theme) ───────── */
    .uppy-Dashboard-inner {
      background: var(--bg-1) !important;
      border-color: var(--rule) !important;
      border-radius: 6px !important;
      font-family: 'JetBrains Mono', monospace !important;
    }
    .uppy-Dashboard-AddFiles {
      border-color: var(--rule-soft) !important;
    }
    .uppy-Dashboard-AddFiles-title,
    .uppy-DashboardContent-title {
      color: var(--ink) !important;
      font-family: 'JetBrains Mono', monospace !important;
    }
    .uppy-Dashboard-AddFiles-title button,
    .uppy-Dashboard-browse { color: var(--primary) !important; }
    .uppy-Dashboard-note  { color: var(--text-mute) !important; }
    .uppy-StatusBar {
      background: var(--bg-2) !important;
      border-top-color: var(--rule) !important;
      border-radius: 0 0 6px 6px !important;
      font-family: 'JetBrains Mono', monospace !important;
    }
    .uppy-StatusBar-statusPrimary { color: var(--text) !important; }
    .uppy-StatusBar-actionCircleBtn { color: var(--ink) !important; }
    .uppy-DashboardContent-bar {
      background: var(--bg-1) !important;
      border-bottom-color: var(--rule) !important;
    }
    .uppy-Dashboard-Item-preview   { background: var(--bg-2) !important; }
    .uppy-Dashboard-Item-name      { color: var(--ink) !important; font-family: 'JetBrains Mono', monospace !important; }
    .uppy-Dashboard-Item-status    { color: var(--text-dim) !important; }
    .uppy-c-btn-primary {
      background: var(--primary) !important;
      color: var(--bg) !important;
      border-color: var(--primary) !important;
      font-family: 'JetBrains Mono', monospace !important;
    }
    .uppy-c-btn-primary:hover {
      background: #38a887 !important;
      border-color: #38a887 !important;
    }
    .uppy-Dashboard-dropFilesHereHint { color: var(--text-dim) !important; }

    /* ── Footer ──────────────────────────────────────────── */
    .foot {
      border-top: 1px solid var(--rule);
      padding: 20px 32px;
      font-size: 11.5px; color: var(--text-mute);
      max-width: 1180px; margin: 0 auto;
    }
  </style>
</head>
<body>

<div class="topbar">
  <div class="topbar-inner">
    <a class="brand" href="http://aither.mb.sun.ac.za/">
      <svg class="brand-mark" viewBox="0 0 22 22" fill="none" xmlns="http://www.w3.org/2000/svg">
        <line x1="3" y1="11" x2="19" y2="11" stroke="currentColor" stroke-width="1.3" opacity=".5"/>
        <circle cx="5"  cy="11" r="3.2" stroke="currentColor" stroke-width="1.4"/>
        <circle cx="11" cy="11" r="3.2" stroke="currentColor" stroke-width="1.4"/>
        <circle cx="17" cy="11" r="3.2" stroke="currentColor" stroke-width="1.4"/>
      </svg>
      abc<span class="brand-dim">-cluster</span>
    </a>
    <span class="top-spacer"></span>
    <a class="top-link" href="/">← Dashboard</a>
  </div>
</div>

<main>
  <div class="eyebrow"><span class="num">01</span>Upload</div>
  <h1>Upload Files <span class="dim">→ cluster storage</span></h1>
  <p class="subtitle">
    Resumable TUS uploads · up to ${var.uppy_max_file_size_mb} MB per file ·
    authentication required for protected namespaces.
  </p>

  <div class="info-bar">
    TUS endpoint: <code>${var.tusd_endpoint}</code>
    · Uploads resume automatically after network interruptions.
    · If uploads are rejected, provide a valid token via the Nomad UI.
  </div>

  <div class="uppy-wrap">
    <div id="uppy"></div>
  </div>
</main>

<div class="foot">
  abc-cluster · African Bioinformatics Computing · files are stored in the cluster object store
</div>

<script type="module">
  import { Uppy, Dashboard, Tus } from "https://releases.transloadit.com/uppy/v4.13.3/uppy.min.mjs";

  new Uppy({
    restrictions: {
      maxFileSize: ${var.uppy_max_file_size_mb} * 1024 * 1024,
    },
  })
    .use(Dashboard, {
      inline:                      true,
      target:                      "#uppy",
      width:                       880,
      height:                      460,
      theme:                       "dark",
      showProgressDetails:         true,
      proudlyDisplayPoweredByUppy: false,
      note:                        "Max ${var.uppy_max_file_size_mb} MB · resumable · all file types accepted",
    })
    .use(Tus, {
      endpoint:    "${var.tusd_endpoint}",
      retryDelays: [0, 1000, 3000, 5000],
      chunkSize:   5 * 1024 * 1024,
    });
</script>

</body>
</html>
EOF
      }

      resources {
        cpu    = 128
        memory = 128
      }

      service {
        name     = "abc-nodes-uppy"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "uppy", "uploads",
          "traefik.enable=true",
          "traefik.http.routers.uppy.rule=Host(`uppy.aither`)",
          "traefik.http.routers.uppy.entrypoints=web",
          "traefik.http.services.uppy.loadbalancer.server.port=8090",
        ]

        check {
          name     = "uppy-http"
          type     = "http"
          path     = "/"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
