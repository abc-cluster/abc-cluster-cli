# Uppy file-upload dashboard — abc-nodes base pack
# Serves a static Uppy Dashboard page backed by the existing tusd TUS server.

job "abc-nodes-uppy" {
  namespace = "abc-services"
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
  type        = "service"

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
        image = [[ var "nginx_image" . | quote ]]
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
  <title>ABC Cluster Uploads</title>
  <link rel="stylesheet" href="https://releases.transloadit.com/uppy/v4.13.3/uppy.min.css">
</head>
<body>
  <div id="uppy"></div>
  <script type="module">
    import { Uppy, Dashboard, Tus } from "https://releases.transloadit.com/uppy/v4.13.3/uppy.min.mjs";
    new Uppy({ restrictions: { maxFileSize: [[ var "uppy_max_file_size_mb" . ]] * 1024 * 1024 } })
      .use(Dashboard, {
        inline: true,
        target: "#uppy",
        width: 780,
        height: 480,
        showProgressDetails: true,
        proudlyDisplayPoweredByUppy: false,
      })
      .use(Tus, {
        endpoint: [[ var "tusd_endpoint" . | quote ]],
        retryDelays: [0, 1000, 3000, 5000],
        chunkSize: 5 * 1024 * 1024,
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
      }
    }
  }
}
