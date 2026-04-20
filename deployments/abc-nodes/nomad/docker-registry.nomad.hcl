# Docker Registry v2 — local OCI image store
# Wave uses this as a pull-through cache / container image mirror.
# Nextflow pipelines can push pipeline-container images here for fast local access.
#
# Registry is HTTP (no TLS) — Docker clients need:
#   /etc/docker/daemon.json: { "insecure-registries": ["100.70.185.46:5000"] }
#
# Deploy:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/docker-registry.nomad.hcl

job "abc-nodes-docker-registry" {
  namespace   = "services"
  type        = "service"
  priority    = 80

  group "registry" {
    count = 1

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
      driver = "containerd-driver"

      config {
        image = "registry:2"
      }

      env {
        REGISTRY_HTTP_ADDR                     = "0.0.0.0:5000"
        REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY = "/var/lib/registry"
        REGISTRY_HTTP_RELATIVEURLS             = "true"
        # Delete API (needed by Wave to evict stale layers)
        REGISTRY_STORAGE_DELETE_ENABLED        = "true"
      }

      # Persist registry data to the host filesystem so images survive rescheduling.
      # The host must have /opt/nomad/data/docker-registry/ writable by the Docker daemon.
      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
