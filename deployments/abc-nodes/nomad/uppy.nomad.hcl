# Uppy file-upload dashboard — abc-nodes floor
# Serves a static Uppy Dashboard page backed by the existing tusd TUS server.
# Users must be on the Tailscale network to resolve *.aither hostnames.

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
  default     = "http://100.70.185.46:8080/files/"
}

variable "uppy_max_file_size_mb" {
  type    = number
  default = 500
}

job "abc-nodes-uppy" {
  namespace = "services"
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
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Upload — aither</title>
  <link rel="stylesheet" href="https://releases.transloadit.com/uppy/v4.13.3/uppy.min.css">
  <style>
    :root {
      --bg:        #0e0e16;
      --surface:   #16161f;
      --border:    #2a2a3d;
      --accent:    #7c6af7;
      --accent-hi: #a08fff;
      --text:      #e2e2f0;
      --muted:     #7878a0;
    }

    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

    html, body {
      height: 100%;
      background: var(--bg);
      color: var(--text);
      font-family: "Inter", "SF Pro Display", -apple-system, BlinkMacSystemFont, sans-serif;
    }

    body {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      min-height: 100vh;
      padding: 2rem 1rem;
      gap: 2rem;
    }

    header {
      text-align: center;
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: .5rem;
    }

    .wordmark {
      display: flex;
      align-items: center;
      gap: .6rem;
      font-size: .8rem;
      font-weight: 600;
      letter-spacing: .14em;
      text-transform: uppercase;
      color: var(--muted);
    }

    .wordmark svg {
      width: 18px; height: 18px;
      fill: var(--accent);
      flex-shrink: 0;
    }

    h1 {
      font-size: clamp(1.6rem, 4vw, 2.2rem);
      font-weight: 700;
      letter-spacing: -.03em;
      background: linear-gradient(135deg, var(--text) 30%, var(--accent-hi));
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
      background-clip: text;
    }

    .subtitle {
      font-size: .875rem;
      color: var(--muted);
    }

    /* Uppy dark-mode overrides */
    .uppy-Dashboard-inner {
      background: var(--surface) !important;
      border-color: var(--border) !important;
      border-radius: 14px !important;
    }
    .uppy-Dashboard-AddFiles {
      border-color: var(--border) !important;
    }
    .uppy-Dashboard-AddFiles-title {
      color: var(--text) !important;
    }
    .uppy-Dashboard-browse {
      color: var(--accent-hi) !important;
    }
    .uppy-StatusBar {
      background: var(--surface) !important;
      border-top-color: var(--border) !important;
      border-radius: 0 0 14px 14px !important;
    }

    footer {
      font-size: .72rem;
      color: var(--muted);
      opacity: .6;
    }
  </style>
</head>
<body>
  <header>
    <div class="wordmark">
      <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
        <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
      </svg>
      aither cluster
    </div>
    <h1>Upload Files</h1>
    <p class="subtitle">Resumable uploads · up to ${var.uppy_max_file_size_mb} MB per file</p>
  </header>

  <div id="uppy"></div>

  <footer>Files are stored securely in the cluster object store.</footer>

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
        width:                       780,
        height:                      480,
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
        provider = "nomad"
        tags = [
          "abc-nodes", "uppy", "upload",
          "traefik.enable=true",
          "traefik.http.routers.uppy.rule=Host(`uppy.aither`)",
          "traefik.http.services.uppy.loadbalancer.server.port=8090",
        ]
      }
    }
  }
}
