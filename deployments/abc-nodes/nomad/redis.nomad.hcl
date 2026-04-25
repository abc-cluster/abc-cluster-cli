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

    # Pin to aither: Redis uses a static port (6379) in host-network mode.
    # Wave connects via 100.70.185.46:6379 (aither's Tailscale IP); if Redis
    # lands on node2 that connection breaks. Redis is ephemeral (no data loss
    # on reschedule) but must stay predictably on aither until Wave is updated
    # to use a Consul-resolved address.
    # Verify with: nomad node status -self  (check "Name" field on aither)
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
