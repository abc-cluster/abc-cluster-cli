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

    # Pin to aither: PostgreSQL data lives on aither's scratch host volume.
    # Must not be rescheduled to a new node — that would start with an empty DB.
    # Verify with: nomad node status -self  (check "Name" field on aither)
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      # bridge mode with static port mapping publishes 5432 on the host.
      # containerd-driver does not support network_mode="host" config option;
      # bridge + static port is the correct way to expose a specific port.
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
