# Wave Lite — container augmentation service (abc-experimental namespace)
#
# PURPOSE
# ───────
# Wave is a Seqera service that augments container images at pull time for
# Nextflow pipelines — injecting Fusion filesystem drivers, adding packages,
# or layering credentials without rebuilding the image.  Wave Lite enables
# the Fusion filesystem (fast S3 access for Nextflow) without requiring the
# full Wave build/mirror/scan infrastructure.
#
# WAVE LITE vs FULL
# ─────────────────
#  Lite (this deployment):             Full Wave (future, requires EKS/AWS):
#  ✓ Container augmentation            ✓ Everything in Lite
#  ✓ Container inspection              ✓ Container Freeze
#  ✓ Fusion filesystem support         ✓ Container Build service
#  ✗ Container Mirror                  ✓ Container Mirror
#  ✗ Container Build                   ✓ Security scanning
#  ✗ Security scanning                 ✓ Blob caching
#  ✗ Blob caching
#
# IMAGE — SELF-BUILD REQUIRED
# ───────────────────────────
# Wave has no public Docker image.  Seqera distributes only via private ECR
# (nf-tower-enterprise/wave) under an enterprise agreement.  The source is
# Apache-2.0 at https://github.com/seqeralabs/wave.
#
# Build and push to the local registry (one-time, re-run to upgrade):
#
#   git clone https://github.com/seqeralabs/wave --branch v1.33.3 --depth 1
#   cd wave
#
#   # Option A — jib direct push (no local Docker required)
#   ./gradlew jib \
#     -Djib.to.image=100.70.185.46:5000/wave:v1.33.3 \
#     -Djib.to.auth.allowInsecureRegistries=true
#
#   # Option B — jib to local Docker, then push
#   ./gradlew jibDockerBuild --image=100.70.185.46:5000/wave:v1.33.3
#   docker push 100.70.185.46:5000/wave:v1.33.3
#
# Local registry: abc-nodes-docker-registry at 100.70.185.46:5000 (HTTP, no TLS).
# containerd on each node must allow this insecure registry — add to:
#   /etc/containerd/config.toml:
#     [plugins."io.containerd.grpc.v1.cri".registry.mirrors."100.70.185.46:5000"]
#       endpoint = ["http://100.70.185.46:5000"]
#
# DEPENDENCIES
# ────────────
#  PostgreSQL  : abc-experimental-postgres  (100.70.185.46:5432, db=abc)
#                → wave_db_init prestart creates the 'wave' database + user
#  Redis       : abc-experimental-redis     (100.70.185.46:6379)
#                → must be enabled: terraform apply -var enable_redis=true
#
# CREDENTIALS
# ───────────
# Store Wave DB password in Nomad Variables before deploying:
#   nomad var put nomad/jobs/abc-experimental-wave \
#     wave_db_password=<choose-a-password>
#
# NEXTFLOW INTEGRATION
# ────────────────────
# In nextflow.config (user's pipeline config):
#   wave {
#     enabled  = true
#     endpoint = 'http://wave.aither'
#   }
#   fusion {
#     enabled = true
#   }
#
# ENDPOINTS
# ─────────
#  API / Nextflow : http://wave.aither  (Traefik → port 9090)
#  Health         : http://wave.aither/health
#  Metrics        : http://wave.aither/metrics  (Prometheus scrape target)

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "wave_version" {
  type    = string
  default = "v1.33.3"
}

variable "wave_image" {
  type        = string
  default     = "100.70.185.46:5000/wave:v1.33.3"
  description = "Self-built Wave image in the local registry. See build instructions in file header."
}

# PostgreSQL superuser credentials (for the db-init prestart only).
# Wave itself connects as wave_user (credentials from Nomad Variables).
variable "pg_host" {
  type    = string
  default = "100.70.185.46"
}
variable "pg_admin_user" {
  type    = string
  default = "abc"
}
variable "pg_admin_password" {
  type    = string
  default = "abc_db_secret"
}
variable "pg_wave_db" {
  type    = string
  default = "wave"
}
variable "pg_wave_user" {
  type    = string
  default = "wave_user"
}

variable "redis_uri" {
  type    = string
  default = "redis://100.70.185.46:6379"
}

variable "wave_server_url" {
  type        = string
  default     = "http://wave.aither"
  description = "Public URL of this Wave instance. Nextflow uses this as the wave.endpoint value."
}

job "abc-experimental-wave" {
  namespace   = "abc-experimental"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"
  priority    = 50

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "wave"
  }

  group "wave" {
    count = 1

    # Pin to aither — PostgreSQL data and Redis are on aither.
    # Wave's DB connections use the static Tailscale IP (100.70.185.46) rather
    # than 127.0.0.1 so the URLs stay valid even if Wave is rescheduled to a
    # different node in future (e.g. when the DB is extracted to a managed service).
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 9090
        to     = 9090
      }
    }

    restart {
      attempts = 3
      delay    = "30s"
      interval = "5m"
      mode     = "delay"
    }

    # ── DB bootstrap (prestart, idempotent) ───────────────────────────────────
    # Creates the 'wave' PostgreSQL database and wave_user if they don't already
    # exist.  Uses the admin `abc` superuser to bootstrap, then Wave connects as
    # the least-privilege wave_user.
    #
    # The DO $$ ... EXCEPTION block makes each step idempotent — safe to re-run
    # on every deployment without error.  Wave's Flyway migrations handle schema
    # setup on first start.
    task "wave-db-init" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      # DESIGN DECISION: containerd-driver so psql comes from the postgres image
      # rather than requiring a host-side psql install.
      driver = "containerd-driver"

      config {
        image      = "postgres:15-alpine"
        entrypoint = ["/bin/sh", "-c"]
        args = [
          <<-EOC
          set -e
          export PGPASSWORD="${var.pg_admin_password}"

          echo "wave-db-init: ensuring wave_user exists..."
          psql -h ${var.pg_host} -U ${var.pg_admin_user} -d ${var.pg_admin_user} -c "
            DO \$\$
            BEGIN
              IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${var.pg_wave_user}') THEN
                CREATE ROLE ${var.pg_wave_user} LOGIN PASSWORD '$WAVE_DB_PASSWORD';
                RAISE NOTICE 'Created role ${var.pg_wave_user}';
              ELSE
                RAISE NOTICE 'Role ${var.pg_wave_user} already exists';
              END IF;
            END
            \$\$;
          "

          echo "wave-db-init: ensuring wave database exists..."
          psql -h ${var.pg_host} -U ${var.pg_admin_user} -d ${var.pg_admin_user} -c "
            SELECT 'CREATE DATABASE ${var.pg_wave_db} OWNER ${var.pg_wave_user}'
            WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${var.pg_wave_db}')
          " | grep -q 'CREATE DATABASE' && \
            psql -h ${var.pg_host} -U ${var.pg_admin_user} -d ${var.pg_admin_user} \
              -c "CREATE DATABASE ${var.pg_wave_db} OWNER ${var.pg_wave_user}" || \
            echo "wave-db-init: database ${var.pg_wave_db} already exists"

          echo "wave-db-init: granting schema privileges..."
          psql -h ${var.pg_host} -U ${var.pg_admin_user} -d ${var.pg_wave_db} -c "
            GRANT USAGE, CREATE ON SCHEMA public TO ${var.pg_wave_user};
            GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ${var.pg_wave_user};
            GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO ${var.pg_wave_user};
            ALTER DEFAULT PRIVILEGES IN SCHEMA public
              GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ${var.pg_wave_user};
            ALTER DEFAULT PRIVILEGES IN SCHEMA public
              GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO ${var.pg_wave_user};
          "

          echo "wave-db-init: done"
          EOC
        ]
      }

      # WAVE_DB_PASSWORD injected from Nomad Variables so the prestart task can
      # set the initial password for wave_user consistently with what Wave itself uses.
      template {
        destination = "secrets/db.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-experimental-wave" -}}
WAVE_DB_PASSWORD={{ .wave_db_password }}
{{- end }}
EOF
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Wave Lite server ──────────────────────────────────────────────────────
    task "wave" {
      driver = "containerd-driver"

      config {
        image = var.wave_image
        # Wave (Micronaut) looks for config.yml in the working directory.
        # MICRONAUT_CONFIG_FILES overrides the search path — see env block below.
      }

      # ── Wave config.yml (delivered via Nomad template) ──────────────────────
      # Micronaut reads this file at startup.  MICRONAUT_CONFIG_FILES env var
      # points to its location in the task dir.
      #
      # DESIGN DECISION: Lite mode only.
      #   wave.build.enabled: false  — no BuildKit, no EKS/EFS dependency
      #   wave.mirror.enabled: false — no container mirror service
      #   wave.scan.enabled: false   — no Trivy/scanner
      #   wave.allowAnonymous: true  — Nextflow pipelines connect without a
      #                                Tower/Seqera Platform auth token.
      #                                Acceptable for a research cluster where
      #                                Fusion access is gated by cluster login.
      #
      # MICRONAUT_ENVIRONMENTS activates profile-specific config blocks:
      #   lite      — disables build/mirror/scan, enables augmentation only
      #   postgres  — activates JDBC datasource config
      #   redis     — activates Lettuce Redis client config
      #   rate-limit — enables pull rate limiting (prevents runaway pipeline pulls)
      #   prometheus — exposes /metrics endpoint for VictoriaMetrics scrape
      template {
        destination = "local/config.yml"
        data        = <<EOF
wave:
  server:
    url: "${var.wave_server_url}"
  allowAnonymous: true
  build:
    enabled: false
  mirror:
    enabled: false
  scan:
    enabled: false
  tokens:
    cache:
      duration: "36h"
  metrics:
    enabled: true

rate-limit:
  pull:
    anonymous: 250/1h
    authenticated: 2000/1m
  timeout-errors:
    max-rate: 100/1m

micronaut:
  netty:
    event-loops:
      default:
        # 2–4× CPU core count. aither has 8+ cores; 16 threads is conservative.
        num-threads: 16
  http:
    services:
      stream-client:
        read-timeout: "30s"
        read-idle-timeout: "5m"

# Disable noisy management endpoints; keep health + metrics.
endpoints:
  env:
    enabled: false
  bean:
    enabled: false
  caches:
    enabled: false
  refresh:
    enabled: false
  loggers:
    enabled: false
  info:
    enabled: false
  health:
    enabled: true
    disk-space:
      enabled: false
    jdbc:
      enabled: false
  metrics:
    enabled: true
EOF
      }

      env {
        # Micronaut profile activation — defines which config blocks are loaded.
        MICRONAUT_ENVIRONMENTS = "lite,rate-limit,redis,postgres,prometheus"

        # Point Micronaut at the config.yml rendered into the task dir above.
        MICRONAUT_CONFIG_FILES = "${NOMAD_TASK_DIR}/config.yml"

        # PostgreSQL — JDBC format required by Micronaut/Hibernate.
        # Note: NOT the postgresql:// URL scheme used by psycopg/libpq.
        WAVE_DB_URI  = "jdbc:postgresql://${var.pg_host}:5432/${var.pg_wave_db}"
        WAVE_DB_USER = "${var.pg_wave_user}"

        # Redis URI — standard redis:// scheme consumed by Lettuce client.
        REDIS_URI = "${var.redis_uri}"

        # AWS SDK requires a region even for non-AWS endpoints.
        AWS_DEFAULT_REGION = "us-east-1"
      }

      # Sensitive values from Nomad Variables.
      template {
        destination = "secrets/wave.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-experimental-wave" -}}
WAVE_DB_PASSWORD={{ .wave_db_password }}
{{- end }}
EOF
      }

      service {
        name     = "abc-experimental-wave"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "wave", "experimental",
          "prometheus.scrape=true",
          "traefik.enable=true",
          "traefik.http.routers.wave.rule=Host(`wave.aither`)",
          "traefik.http.routers.wave.entrypoints=web",
          "traefik.http.services.wave.loadbalancer.server.port=9090",
        ]

        check {
          name     = "wave-health"
          type     = "http"
          path     = "/health"
          interval = "20s"
          timeout  = "5s"
          # Wave takes 30–60s to initialise (Flyway DB migration + JVM warmup).
          # check_restart gives it time before Nomad considers it failed.
          check_restart {
            limit           = 5
            grace           = "90s"
            ignore_warnings = false
          }
        }
      }

      resources {
        # Micronaut JVM + netty event loop. 1500 MB is the minimum per Seqera docs.
        # Wave Lite without build overhead sits comfortably at 800–1000 MB steady state.
        cpu    = 500
        memory = 1536
      }
    }
  }
}
