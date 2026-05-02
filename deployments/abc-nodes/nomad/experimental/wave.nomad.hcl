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
# DEPLOYMENT NODE: nomad02 in sun-nomadlab (Tailscale IP 100.126.253.95)
# ─────────────────────────────────────────────────────────────────────────────
# Wave runs on nomad02 because it has the `docker` driver which natively supports
# HTTP registries via insecure_registries in task config.  aither uses the
# containerd-driver which does not support HTTP registries without complex
# host-level patching that proved unreliable.
#
# IMAGE — SELF-BUILD REQUIRED
# ───────────────────────────
# Wave has no public Docker image.  Seqera distributes only via private ECR
# (nf-tower-enterprise/wave) under an enterprise agreement.  The source is
# Apache-2.0 at https://github.com/seqeralabs/wave.
#
# Build on your Mac and push to the GCP local registry (one-time, re-run to upgrade):
#
#   git clone https://github.com/seqeralabs/wave --branch v1.33.3 --depth 1
#   cd wave
#
#   # Option A — build on Mac, push via jib + SSH tunnel to nomad02
#   ssh -f -N -L 5000:localhost:5000 nomad02  # tunnel Mac:5000 → nomad02:5000
#   JAVA_HOME=/usr/local/opt/openjdk ./gradlew jib \
#     -PjibRepo=localhost:5000 \
#     -Djib.to.image=localhost:5000/wave:v1.33.3
#
#   # Option B — build on Mac, push via Docker + tunnel
#   ./gradlew jibDockerBuild --image=localhost:5000/wave:v1.33.3
#   ssh -f -N -L 5000:localhost:5000 nomad02
#   docker push localhost:5000/wave:v1.33.3
#
#   # Option C — copy from aither's old registry via the copy batch job:
#   abc admin services nomad cli -- job run /tmp/copy-wave-image-to-nomad02.nomad.hcl
#
# nomad02 local registry: abc-nodes-docker-registry on nomad02:5000 (HTTP, no TLS).
# Docker on nomad02 allows HTTP via insecure_registries in /etc/docker/daemon.json
# (set up by the setup-nomad02-docker-insecure-registry batch job).
#
# DEPENDENCIES
# ────────────
#  PostgreSQL  : abc-experimental-postgres  (100.70.185.46:5432, db=abc)
#                → wave_db_init prestart creates the 'wave' database + user
#                  (postgres stays on aither; accessed via Tailscale IP)
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
  default     = "100.126.253.95:5000/wave:v1.33.3"
  description = "Wave image in nomad02's local HTTP registry. See build instructions in file header. Use copy-wave-image-to-nomad02 batch job to seed it from aither's registry."
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

variable "pg_wave_password" {
  type        = string
  default     = "wave_secret"
  description = "Password for wave_user in PostgreSQL. Override with -var pg_wave_password=... at deploy time."
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

    # Pin to nomad02 (sun-nomadlab, Tailscale IP 100.126.253.95).
    # This node runs the docker driver and hosts the local Docker registry at port 5000.
    # PostgreSQL (100.70.185.46:5432) and Redis (100.70.185.46:6379) stay on
    # aither and are reached via their static Tailscale IPs.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad02"
    }

    network {
      mode = "host"
      port "http" { static = 9090 }
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

      # Uses the docker driver — psql comes from the postgres image rather than
      # requiring a host-side psql install.  postgres:15-alpine is a public image
      # so the docker driver pulls it from Docker Hub with no registry config needed.
      driver = "docker"

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

      env {
        WAVE_DB_PASSWORD = "${var.pg_wave_password}"
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Wave Lite server ──────────────────────────────────────────────────────
    task "wave" {
      driver = "docker"

      config {
        image        = var.wave_image
        network_mode = "host"
        # Wave (Micronaut) looks for config.yml in the working directory.
        # MICRONAUT_CONFIG_FILES overrides the search path — see env block below.
        #
        # HTTP pull from 100.126.253.95:5000 is allowed via /etc/docker/daemon.json
        # on nomad02 (set by setup-nomad02-docker-insecure-registry batch job).
        # network_mode=host: docker driver on nomad02 doesn't apply group-level host
        # mode automatically — this ensures Wave binds directly to host port 9090.
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
        # 2–4× CPU core count. nomad02 has 4+ vCPUs; 16 threads is conservative.
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

        WAVE_DB_PASSWORD = "${var.pg_wave_password}"
      }

      resources {
        # Micronaut JVM + netty event loop. 1500 MB is the minimum per Seqera docs.
        # Wave Lite without build overhead sits comfortably at 800–1000 MB steady state.
        cpu    = 500
        memory = 1536
      }
    }

    # ── Consul service registration sidecar ───────────────────────────────────
    # nomad02 has no Consul agent, so we can't use `service { provider = "consul" }`.
    # This raw_exec sidecar registers Wave directly against aither's Consul agent
    # (100.70.185.46:8500) with nomad02's Tailscale IP (100.126.253.95:9090) as the
    # address, then deregisters cleanly on stop.
    # Traefik on aither reads Consul catalog and routes wave.aither → this address.
    task "consul-register" {
      lifecycle {
        hook    = "poststart"
        sidecar = true
      }

      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["${NOMAD_TASK_DIR}/register.sh"]
      }

      template {
        destination = "local/register.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/bash
set -e

CONSUL_ADDR="http://100.70.185.46:8500"
SERVICE_ID="abc-experimental-wave-nomad02"
WAVE_ADDR="100.126.253.95"
WAVE_PORT=9090

# Register on start, deregister on stop (SIGTERM from Nomad).
cleanup() {
  echo "consul-register: deregistering $SERVICE_ID"
  curl -sf -X PUT "$CONSUL_ADDR/v1/agent/service/deregister/$SERVICE_ID" || true
  exit 0
}
trap cleanup SIGTERM SIGINT

echo "consul-register: registering $SERVICE_ID in Consul at $CONSUL_ADDR"
# No local readiness wait — Consul's own HTTP health check (below) handles this.
# Consul marks the service critical until Wave's /health endpoint responds,
# so Traefik won't route to it until it's actually up.
curl -sf -X PUT "$CONSUL_ADDR/v1/agent/service/register" \
  -H "Content-Type: application/json" \
  -d "{
    \"ID\": \"$SERVICE_ID\",
    \"Name\": \"abc-experimental-wave\",
    \"Address\": \"$WAVE_ADDR\",
    \"Port\": $WAVE_PORT,
    \"Tags\": [
      \"abc-nodes\", \"wave\", \"experimental\",
      \"prometheus.scrape=true\",
      \"traefik.enable=true\",
      \"traefik.http.routers.wave.rule=Host(\\\"wave.aither\\\")\",
      \"traefik.http.routers.wave.entrypoints=web\",
      \"traefik.http.services.wave.loadbalancer.server.port=9090\"
    ],
    \"Check\": {
      \"HTTP\": \"http://$WAVE_ADDR:$WAVE_PORT/health\",
      \"Interval\": \"20s\",
      \"Timeout\": \"5s\",
      \"DeregisterCriticalServiceAfter\": \"2m\"
    }
  }"

echo "consul-register: registered. Sleeping until stopped..."
# Sleep in a loop so the trap fires promptly on SIGTERM.
while true; do sleep 30; done
EOF
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }
  }
}
