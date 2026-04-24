# PostgreSQL — relational DB (Wave dependency)

job "abc-nodes-postgres" {
  namespace = [[ var "services_namespace" . | quote ]]
  type      = "service"
  priority  = 80

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
        image = [[ var "postgres_image" . | quote ]]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        POSTGRES_DB       = [[ var "postgres_db" . | quote ]]
        POSTGRES_USER     = [[ var "postgres_user" . | quote ]]
        POSTGRES_PASSWORD = [[ var "postgres_password" . | quote ]]
        PGDATA            = [[ var "postgres_pgdata" . | quote ]]
      }

      resources {
        cpu    = 300
        memory = 512
      }
    }
  }
}
