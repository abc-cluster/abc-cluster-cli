# Wave — container-build orchestrator (abc-experimental namespace)
# WIP: resumes when Wave release channel publishes a stable image.
#
# Dependencies: postgres (build metadata), redis (rate-limit / job queue)
# Both must be running in abc-experimental before enabling this job.
#
# Enable via Terraform (after postgres + redis are up):
#   terraform apply -var enable_postgres=true -var enable_redis=true -var enable_wave=true
#
# Image: TBD — set wave_image in terraform vars once Wave publishes to a registry.
#   Current placeholder prevents accidental deploy with an empty image.
#
# Required env vars (set via Nomad Variables or tfvars before enabling):
#   WAVE_DATABASE_URL  — postgres connection string
#   WAVE_REDIS_URL     — redis connection string
#   WAVE_SECRET_KEY    — application secret (generate with: openssl rand -hex 32)

job "abc-experimental-wave" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 50

  group "wave" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 8765
        to     = 8765
      }
    }

    restart {
      attempts = 3
      delay    = "30s"
      interval = "5m"
      mode     = "delay"
    }

    task "wave" {
      driver = "containerd-driver"

      # TODO: replace with published Wave image once available
      # e.g. "ghcr.io/wandb/wave:latest" or equivalent
      config {
        image = "PLACEHOLDER_WAVE_IMAGE"
      }

      env {
        # PostgreSQL — must match abc-experimental-postgres configuration
        WAVE_DATABASE_URL = "postgresql://abc:abc_db_secret@127.0.0.1:5432/abc"

        # Redis — must match abc-experimental-redis configuration
        WAVE_REDIS_URL = "redis://127.0.0.1:6379/0"

        WAVE_PORT = "8765"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
