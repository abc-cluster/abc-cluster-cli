# Wave by Seqera — container image build & caching service for Nextflow
#
# ┌─ IMPORTANT: PRIVATE IMAGE ─────────────────────────────────────────────────
# │ The Wave container image is on Seqera's private AWS ECR registry.
# │ It is NOT publicly available on ghcr.io or cr.seqera.io.
# │
# │ To obtain the image URI and AWS credentials:
# │   1. Contact Seqera support: support@seqera.io
# │   2. Request Wave image access for self-hosted deployment (v1.33.2)
# │   3. You will receive an image URI + scoped AWS IAM credentials
# │   4. Store them in Nomad Variables (see CREDENTIALS below) and redeploy
# │      with -var="wave_image=<uri>"
# └────────────────────────────────────────────────────────────────────────────
#
# Runs in "lite" mode: single JVM process, no Kubernetes, no Tower.
# Depends on: abc-nodes-postgres (port 5432), abc-nodes-redis (port 6379)
#
# Environments activated:
#   lite        — standalone (no Tower/Platform)
#   rate-limit  — per-token build rate limiting
#   redis       — Redis-backed rate limit storage
#   postgres    — PostgreSQL-backed metadata storage
#   prometheus  — expose /metrics on port 9091
#
# CREDENTIALS (Nomad Variables, namespace: services)
# ───────────────────────────────────────────────────
#  Path: nomad/jobs/abc-nodes-wave
#  Keys: aws_access_key_id, aws_secret_access_key
#
#  Store / rotate:
#    abc admin services nomad cli -- var put -namespace services \
#      nomad/jobs/abc-nodes-wave \
#      aws_access_key_id=<id> \
#      aws_secret_access_key=<secret>
#
# Deploy (once you have the image URI from Seqera):
#   abc admin services nomad cli -- job run \
#     -var="wave_image=195996028523.dkr.ecr.eu-west-1.amazonaws.com/nf-tower-enterprise/wave:v1.33.2" \
#     deployments/abc-nodes/nomad/wave.nomad.hcl

variable "wave_image" {
  type        = string
  # Replace with the URI provided by Seqera support.
  # ECR format: 195996028523.dkr.ecr.eu-west-1.amazonaws.com/nf-tower-enterprise/wave:<tag>
  default     = "PENDING_SEQERA_IMAGE_URI"
  description = "Full Wave container image URI (private ECR — set via -var at deploy time)"
}

job "abc-nodes-wave" {
  namespace   = "services"
  type        = "service"
  priority    = 80

  group "wave" {
    count = 1

    network {
      mode = "host"
      port "http"    { static = 9090 }
      port "metrics" { static = 9091 }
    }

    restart {
      attempts = 5
      delay    = "20s"
      interval = "2m"
      mode     = "delay"
    }

    # ─────────────────────────────────────────────────────────────────────────
    # Prestart: authenticate to Seqera's private ECR and pre-pull the Wave
    # image into the local containerd store.  Credentials are read from Nomad
    # Variables at runtime — no secrets in the job spec.
    #
    # ECR tokens are valid for 12 hours; this task re-runs on every alloc
    # restart so the token is always fresh.
    #
    # Requires aws-cli on the Nomad client:
    #   snap install aws-cli --classic
    # ─────────────────────────────────────────────────────────────────────────
    task "wave-pull" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args    = ["-c", "${NOMAD_TASK_DIR}/wave-pull.sh"]
      }

      template {
        destination = "${NOMAD_TASK_DIR}/wave-pull.sh"
        perms       = "755"
        data        = <<-EOF
#!/bin/sh
set -e
{{ with nomadVar "nomad/jobs/abc-nodes-wave" -}}
IMAGE="${var.wave_image}"

echo "==> Authenticating to AWS ECR..."
ECR_TOKEN=$(AWS_ACCESS_KEY_ID="{{ .aws_access_key_id }}" \
            AWS_SECRET_ACCESS_KEY="{{ .aws_secret_access_key }}" \
            AWS_DEFAULT_REGION="eu-west-1" \
            aws ecr get-login-password --region eu-west-1)

echo "==> Logging in to ECR..."
echo "${ECR_TOKEN}" | \
  /usr/local/bin/nerdctl login 195996028523.dkr.ecr.eu-west-1.amazonaws.com \
    --username AWS --password-stdin

echo "==> Pulling ${IMAGE}..."
/usr/local/bin/nerdctl pull "${IMAGE}"
echo "==> Wave image ready."
{{- end }}
EOF
      }

      resources {
        cpu    = 300
        memory = 256
      }
    }

    # ─────────────────────────────────────────────────────────────────────────
    # Main task: Wave — runs against the image pre-pulled by wave-pull above.
    # ─────────────────────────────────────────────────────────────────────────
    task "wave" {
      driver = "containerd-driver"

      config {
        image = var.wave_image
      }

      env {
        MICRONAUT_ENVIRONMENTS = "lite,rate-limit,redis,postgres,prometheus"

        # HTTP server
        MICRONAUT_SERVER_PORT = "9090"

        # PostgreSQL — connects to Supabase PostgreSQL (abc-nodes-supabase job)
        WAVE_DB_URI  = "jdbc:postgresql://100.70.185.46:5432/wave"
        WAVE_DB_USER = "wave"
        # WAVE_DB_PASSWORD injected via template below (from nomadVar)

        # Redis
        REDIS_URI = "redis://100.70.185.46:6379"

        # Wave Lite self-reference
        WAVE_SERVER_URL = "http://100.70.185.46:9090"

        # Local Docker registry mirror (port 5000)
        WAVE_REGISTRY_URL = "http://100.70.185.46:5000"

        # Prometheus metrics
        MICRONAUT_METRICS_ENABLED                   = "true"
        MICRONAUT_METRICS_EXPORT_PROMETHEUS_ENABLED = "true"
        MICRONAUT_METRICS_EXPORT_PROMETHEUS_STEP    = "60s"

        # JVM — Wave needs 2-4 GB heap
        JAVA_OPTS = "-Xms1g -Xmx3g -XX:+UseG1GC -XX:MaxGCPauseMillis=200"
      }

      # Wave DB password — read from Nomad Variable (set by init-supabase-secrets.sh).
      # Falls back to the old default so existing deployments aren't broken until
      # the Variable is stored and the job is redeployed.
      template {
        destination = "secrets/wave-db.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-wave" -}}
WAVE_DB_PASSWORD={{ .wave_db_password }}
{{- else -}}
WAVE_DB_PASSWORD=wave_db_secret
{{- end }}
EOF
      }

      resources {
        cpu    = 1000
        memory = 3500
      }

      service {
        name     = "abc-nodes-wave"
        port     = "http"
        provider = "nomad"

        check {
          type     = "http"
          path     = "/health"
          interval = "30s"
          timeout  = "10s"
        }

        tags = [
          "abc-nodes", "wave", "seqera",
        ]
      }
    }
  }
}
