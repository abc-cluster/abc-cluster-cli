# PostgreSQL — relational DB (abc-experimental namespace)
# Shared dependency for Wave (build metadata) and Supabase.
# Data persisted to /scratch/abc-postgres/pgdata on the host node.
#
# Enable via Terraform:
#   terraform apply -var enable_postgres=true
#
# Or via abc CLI (manual):
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/experimental/postgres.nomad.hcl

job "abc-experimental-postgres" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 80

  group "postgres" {
    count = 1

    # Pin to aither: PostgreSQL data lives on aither's scratch host volume.
    # Must not be rescheduled to a new node — that would start with an empty DB.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "pg" {
        static = 5432
        to     = 5432
      }
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
        POSTGRES_DB       = "abc"
        POSTGRES_USER     = "abc"
        POSTGRES_PASSWORD = "abc_db_secret"
        PGDATA            = "/scratch/abc-postgres/pgdata"
      }

      resources {
        cpu    = 300
        memory = 512
      }
    }
  }
}
