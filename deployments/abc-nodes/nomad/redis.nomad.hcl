# Redis — in-memory cache (Wave dependency)
# Single node, ephemeral storage (data lost on reschedule — Wave only uses Redis for
# short-lived rate-limit windows, so persistence is not required).
#
# Deploy:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/redis.nomad.hcl

job "abc-nodes-redis" {
  namespace   = "services"
  type        = "service"
  priority    = 80

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
        image   = "redis:7-alpine"
        args    = ["--save", "", "--appendonly", "no", "--maxmemory", "256mb", "--maxmemory-policy", "allkeys-lru"]
      }

      resources {
        cpu    = 200
        memory = 300
      }
    }
  }
}
