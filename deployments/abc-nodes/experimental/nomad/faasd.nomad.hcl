# faasd / OpenFaaS gateway (single-node lab) — abc-nodes
#
# ╔═══════════════════════════════════════════════════════════════════════════╗
# ║  STATUS: ON HOLD — see BLOCKERS section below before attempting deploy   ║
# ╚═══════════════════════════════════════════════════════════════════════════╝
#
# INTENT
# ──────
# Run faasd (lightweight OpenFaaS, https://github.com/openfaas/faasd) as a
# Nomad raw_exec task so that MinIO S3 events can trigger serverless functions
# (e.g. pipeline auto-trigger on file upload, post-processing, notification).
#
# BLOCKERS (must resolve before re-enabling)
# ──────────────────────────────────────────
# 1. Container image availability
#    faasd starts gateway / queue-worker / nats / prometheus as containerd tasks
#    internally.  The ghcr.io/openfaas images are public but ghcr.io returns
#    404 for outdated tags and requires pull-through auth for newer ones.
#    Action: ensure nerdctl can pull the images listed in docker-compose.yaml
#    below before deploying (see "GHCR PRE-PULL" section).
#
# 2. Nomad faasd-provider binary path
#    faasd registers itself as a containerd shim at /usr/local/bin/faasd.
#    The startup script already handles this (cp binary → /usr/local/bin/faasd)
#    but a competing Nomad upgrade or node wipe could lose it.
#
# 3. Nomad HCL / shell interpolation mismatch
#    In Nomad job specs, ${VAR} in any string is HCL interpolation.
#    Shell variables must use $VAR (no braces) inside template.data blocks.
#    The startup script has been rewritten to respect this; do not add ${} in
#    template.data content.
#
# 4. Port conflict with tusd
#    tusd owns host port 8080.  faasd gateway must use port 8089.
#    The embedded docker-compose.yaml maps "8089:8080" for the gateway.
#
# 5. resolv.conf
#    Nomad creates task-specific resolv.conf only for bridge-mode tasks.
#    For host-mode raw_exec the faasd-secrets prestart creates it at
#    <alloc-dir>/faasd/resolv.conf so runc's OCI spec mount succeeds.
#
# ARCHITECTURE
# ────────────
# faasd binary (raw_exec, host network) manages these containerd tasks:
#   gateway      ghcr.io/openfaas/gateway:0.27.13     port 8089→8080
#   queue-worker ghcr.io/openfaas/queue-worker:0.14.2 (internal)
#   nats         nats-streaming:0.25.6                 (internal)
#   prometheus   prom/prometheus:v3.7.3                (internal)
#
# MinIO → faasd event flow (once deployed):
#   MinIO bucket PUT event → webhook POST → faasd gateway :8089 →
#   /function/<name> → user function container
#
#   Setup script: deployments/abc-nodes/experimental/scripts/setup-minio-faasd-events.sh
#
# GHCR PRE-PULL (do this on aither before deploying)
# ───────────────────────────────────────────────────
#   # Optional login (rate-limit relief for public images):
#   echo <PAT> | /usr/local/bin/nerdctl login ghcr.io -u <github-user> --password-stdin
#
#   /usr/local/bin/nerdctl pull ghcr.io/openfaas/gateway:0.27.13
#   /usr/local/bin/nerdctl pull ghcr.io/openfaas/queue-worker:0.14.2
#   /usr/local/bin/nerdctl pull nats-streaming:0.25.6
#   /usr/local/bin/nerdctl pull prom/prometheus:v3.7.3
#
# CREDENTIALS (Nomad Variables, namespace: services)
# ───────────────────────────────────────────────────
#  Path: nomad/jobs/abc-nodes-faasd
#  Keys: admin_password   — OpenFaaS gateway basic-auth password
#        ghcr_username    — GitHub username (optional, for rate-limit relief)
#        ghcr_token       — GitHub PAT with read:packages scope (optional)
#
#  Store:
#    abc admin services nomad cli -- var put -namespace services -force \
#      nomad/jobs/abc-nodes-faasd \
#      admin_password=<pw> ghcr_username=<user> ghcr_token=<pat>
#
# PORTS
# ─────
#  8089 — OpenFaaS gateway (function invocation API, UI, MinIO webhook target)
#  8090 — faasd provider  (internal; accessible for debugging)
#
# DEPLOY (once blockers are cleared)
# ────────────────────────────────────
#   abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/faasd.nomad.hcl

job "abc-nodes-faasd" {
  namespace   = "services"
  type        = "service"
  priority    = 80

  # ── Stop job immediately until blockers are cleared ──────────────────────
  # Uncomment the group block below only after the GHCR PRE-PULL steps above
  # have been completed on aither.
  # ─────────────────────────────────────────────────────────────────────────

  group "faasd" {
    count = 0   # <── set to 1 when ready to deploy

    network {
      mode = "host"
      port "gateway"  { static = 8089 }   # 8080 is taken by tusd
      port "provider" { static = 8090 }
    }

    restart {
      attempts = 5
      delay    = "20s"
      interval = "2m"
      mode     = "delay"
    }

    # ─────────────────────────────────────────────────────────────────────────
    # Prestart 1: pre-pull ghcr.io images
    # ─────────────────────────────────────────────────────────────────────────
    task "faasd-ghcr-pull" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
      driver = "raw_exec"
      config {
        command = "/bin/sh"
        args    = ["-c", "${NOMAD_TASK_DIR}/ghcr-pull.sh"]
      }
      template {
        destination = "${NOMAD_TASK_DIR}/ghcr-pull.sh"
        perms       = "755"
        data        = <<-EOF
#!/bin/sh
# Pre-pull ghcr.io images. Auth is optional (public images); only needed for
# rate-limit relief on busy nodes.
{{ with nomadVar "nomad/jobs/abc-nodes-faasd" -}}
{{ if and .ghcr_username .ghcr_token -}}
echo "==> Logging into ghcr.io as {{ .ghcr_username }}..."
echo '{{ .ghcr_token }}' | \
  /usr/local/bin/nerdctl login ghcr.io \
    --username '{{ .ghcr_username }}' \
    --password-stdin || echo "WARN: ghcr.io login failed — continuing"
{{- end }}
{{- end }}
echo "==> Pulling faasd images..."
/usr/local/bin/nerdctl pull ghcr.io/openfaas/gateway:0.27.13      || echo "WARN: gateway pull failed"
/usr/local/bin/nerdctl pull ghcr.io/openfaas/queue-worker:0.14.2  || echo "WARN: queue-worker pull failed"
/usr/local/bin/nerdctl pull nats-streaming:0.25.6                  || echo "WARN: nats pull failed"
/usr/local/bin/nerdctl pull prom/prometheus:v3.7.3                 || echo "WARN: prometheus pull failed"
echo "==> Pre-pull done."
EOF
      }
      resources {
        cpu    = 300
        memory = 256
      }
    }

    # ─────────────────────────────────────────────────────────────────────────
    # Prestart 2: write basic-auth secrets + resolv.conf
    # ─────────────────────────────────────────────────────────────────────────
    task "faasd-secrets" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
      driver = "raw_exec"
      config {
        command = "/bin/sh"
        args    = ["-c", "${NOMAD_TASK_DIR}/write-secrets.sh"]
      }
      template {
        destination = "${NOMAD_TASK_DIR}/write-secrets.sh"
        perms       = "755"
        data        = <<-EOF
#!/bin/sh
set -e
mkdir -p /var/lib/faasd/secrets
{{ with nomadVar "nomad/jobs/abc-nodes-faasd" -}}
printf '%s' 'admin'               > /var/lib/faasd/secrets/basic-auth-user
printf '%s' '{{ .admin_password }}' > /var/lib/faasd/secrets/basic-auth-password
{{- end }}
chmod 600 /var/lib/faasd/secrets/basic-auth-user \
          /var/lib/faasd/secrets/basic-auth-password
echo "==> faasd secrets written."

# Create resolv.conf in the faasd main task's directory.
# faasd OCI specs bind-mount resolv.conf from the Nomad task dir;
# Nomad only creates it for bridge-mode tasks, not host-mode raw_exec.
# $NOMAD_TASK_DIR (no braces) avoids HCL interpolation — shell resolves it.
FAASD_DIR=$(dirname $(dirname $NOMAD_TASK_DIR))/faasd
mkdir -p "$FAASD_DIR"
cp /etc/resolv.conf "$FAASD_DIR/resolv.conf"
echo "==> resolv.conf written to $FAASD_DIR/resolv.conf"
EOF
      }
      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ─────────────────────────────────────────────────────────────────────────
    # Main task: faasd binary
    # ─────────────────────────────────────────────────────────────────────────
    task "faasd" {
      driver = "raw_exec"

      artifact {
        source      = "https://github.com/openfaas/faasd/releases/download/0.19.7/faasd"
        destination = "${NOMAD_TASK_DIR}/faasd"
        mode        = "file"
      }

      # Embedded docker-compose.yaml — port 8089:8080 avoids conflict with tusd.
      # Image tags match faasd 0.19.7 runtime requirements.
      template {
        destination = "${NOMAD_TASK_DIR}/docker-compose.yaml"
        data        = <<-COMPOSE
version: "3.7"
services:
  prometheus:
    image: prom/prometheus:v3.7.3
    volumes:
      - type: bind
        source: /var/lib/faasd/prometheus.yml
        target: /etc/prometheus/prometheus.yml
    cap_add:
      - NET_ADMIN

  nats:
    image: nats-streaming:0.25.6
    command:
      - "/nats-streaming-server"
      - "-m"
      - "8222"
      - "--store=memory"
      - "--cluster_id=faas-cluster"
    cap_add:
      - NET_ADMIN

  queue-worker:
    image: ghcr.io/openfaas/queue-worker:0.14.2
    environment:
      ack_wait: "5m5s"
      max_inflight: "1"
      write_debug: "false"
      basic_auth: "true"
      secret_mount_path: "/var/lib/faasd/secrets"
      gateway_invoke: "true"
      faas_gateway_address: "127.0.0.1"
    cap_add:
      - NET_ADMIN

  gateway:
    image: ghcr.io/openfaas/gateway:0.27.13
    environment:
      functions_provider_url: "http://faasd-provider:8090/"
      direct_functions: "false"
      direct_functions_suffix: ""
      prometheus_host: "prometheus"
      prometheus_port: "9090"
      faas_prometheus_host: "prometheus"
      faas_prometheus_port: "9090"
      nats_address: "nats"
      nats_port: "4222"
      nats_stream_workers: "5"
      nats_channel: "faas-request"
      auth_pass_body: "false"
      scale_from_zero: "true"
      max_idle_conns: "1024"
      max_idle_conns_per_host: "1024"
      basic_auth: "true"
      secret_mount_path: "/var/lib/faasd/secrets"
    ports:
      - "8089:8080"
    cap_add:
      - NET_ADMIN
        COMPOSE
      }

      template {
        destination = "${NOMAD_TASK_DIR}/prometheus.yml"
        data        = <<-PROM
global:
  scrape_interval:     15s
  evaluation_interval: 15s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: [localhost:9090]
  - job_name: gateway
    scrape_interval: 5s
    metrics_path: /metrics
    static_configs:
      - targets: [gateway:8080]
        PROM
      }

      # start.sh: use $VAR (no braces) — HCL only interpolates ${VAR}.
      template {
        destination = "${NOMAD_TASK_DIR}/start.sh"
        perms       = "755"
        data        = <<-TMPL
#!/bin/sh
set -e
chmod +x $NOMAD_TASK_DIR/faasd
cp $NOMAD_TASK_DIR/faasd /usr/local/bin/faasd
chmod +x /usr/local/bin/faasd
mkdir -p /var/lib/faasd
cp $NOMAD_TASK_DIR/docker-compose.yaml /var/lib/faasd/docker-compose.yaml
cp $NOMAD_TASK_DIR/prometheus.yml      /var/lib/faasd/prometheus.yml
exec /usr/local/bin/faasd up
        TMPL
      }

      config {
        command = "${NOMAD_TASK_DIR}/start.sh"
      }

      env {
        secret_mount_path = "/var/lib/faasd/secrets"
        basic_auth        = "true"
      }

      resources {
        cpu    = 512
        memory = 1024
      }

      service {
        name     = "abc-nodes-faasd"
        port     = "gateway"
        provider = "nomad"

        check {
          type     = "http"
          path     = "/healthz"
          interval = "15s"
          timeout  = "5s"
        }

        tags = ["abc-nodes", "faasd", "openfaas"]
      }
    }
  }
}
