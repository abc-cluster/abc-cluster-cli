# Docker Registry v2 — local OCI image store
# Wave uses this as a pull-through cache / container image mirror.
# Nextflow pipelines can push pipeline-container images here for fast local access.
#
# DEPLOYMENT NODE: nomad02 in sun-nomadlab (Tailscale IP 100.126.253.95)
#   → Uses the `docker` driver which natively handles HTTP registries
#     via insecure_registries — no TLS proxy or iptables tricks needed.
#
# Registry is HTTP (no TLS).  nomad02's Docker daemon must be configured to
# allow insecure pulls from aither's registry (100.70.185.46:5000) so that the
# wave-image-copy batch job can seed this registry.  Run the setup batch job first:
#   abc admin services nomad cli -- job run /tmp/setup-nomad02-docker-insecure-registry.nomad.hcl
#
# Image clients (Wave, other jobs on nomad02) reference:
#   100.126.253.95:5000/<image>:<tag>
# The docker driver accepts insecure_registries = ["100.126.253.95:5000"] in task config.
#
# Deploy:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/docker-registry.nomad.hcl

job "abc-nodes-docker-registry" {
  namespace   = "abc-services"
  type        = "service"
  priority    = 80

  group "registry" {
    count = 1

    # Pin to nomad02 (sun-nomadlab). Tailscale IP: 100.126.253.95
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad02"
    }

    network {
      mode = "host"
      port "registry" { static = 5000 }
    }

    restart {
      attempts = 3
      delay    = "15s"
      interval = "1m"
      mode     = "delay"
    }

    task "registry" {
      driver = "docker"

      config {
        image        = "registry:2"
        network_mode = "host"
        volumes      = ["/opt/nomad/data/docker-registry:/var/lib/registry"]
      }

      env {
        REGISTRY_HTTP_ADDR                        = "0.0.0.0:5000"
        REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY = "/var/lib/registry"
        REGISTRY_HTTP_RELATIVEURLS                = "true"
        # Delete API (needed by Wave to evict stale layers)
        REGISTRY_STORAGE_DELETE_ENABLED           = "true"
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
