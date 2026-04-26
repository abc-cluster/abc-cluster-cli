# Redis — in-memory cache (abc-experimental namespace)
# Dependency for Wave (rate-limit windows, job queue).
# Ephemeral storage — data is intentionally not persisted across restarts.
#
# Enable via Terraform:
#   terraform apply -var enable_redis=true
#
# Or via abc CLI (manual):
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/experimental/redis.nomad.hcl

job "abc-experimental-redis" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 80

  group "redis" {
    count = 1

    # Pin to aither: static port 6379 exposed on aither's Tailscale IP.
    # Wave connects to 100.70.185.46:6379; if Redis lands on another node
    # that connection breaks. Update Wave's REDIS_URL if this changes.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

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
        image = "redis:7-alpine"
        args  = ["--save", "", "--appendonly", "no", "--maxmemory", "256mb", "--maxmemory-policy", "allkeys-lru"]
      }

      resources {
        cpu    = 200
        memory = 300
      }
    }
  }
}
