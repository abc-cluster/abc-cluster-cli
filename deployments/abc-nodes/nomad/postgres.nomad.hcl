# PostgreSQL — relational DB (Wave dependency)
# Stores Wave container build metadata, cache records, job state.
# Data persisted to /opt/nomad/data/wave-postgres on the host node.
#
# Deploy:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/postgres.nomad.hcl

job "abc-nodes-postgres" {
  namespace   = "services"
  type        = "service"
  priority    = 80

  group "postgres" {
    count = 1

    network {
      mode = "host"
      port "pg" { static = 5432 }
    }

    restart {
      attempts = 3
      delay    = "15s"
      interval = "1m"
      mode     = "delay"
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    task "postgres" {
      driver = "containerd-driver"

      config {
        image = "postgres:15-alpine"
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        POSTGRES_DB       = "wave"
        POSTGRES_USER     = "wave"
        POSTGRES_PASSWORD = "wave_db_secret"
        PGDATA            = "/scratch/wave-postgres/pgdata"
      }

      resources {
        cpu    = 300
        memory = 512
      }
    }
  }
}
