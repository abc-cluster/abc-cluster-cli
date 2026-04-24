# Redis — in-memory cache (Wave dependency)

job "abc-nodes-redis" {
  namespace = [[ var "services_namespace" . | quote ]]
  type      = "service"
  priority  = 80

  group "redis" {
    count = 1

    network {
      mode = "host"
      port "redis" { static = 6379 }
    }

    restart {
      attempts = 3
      delay    = "15s"
      interval = "1m"
      mode     = "delay"
    }

    task "redis" {
      driver = "containerd-driver"

      config {
        image = [[ var "redis_image" . | quote ]]
        args  = ["--save", "", "--appendonly", "no", "--maxmemory", "256mb", "--maxmemory-policy", "allkeys-lru"]
      }

      resources {
        cpu    = 200
        memory = 300
      }
    }
  }
}
