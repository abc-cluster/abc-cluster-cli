# Docker Registry v2 — local OCI image store

job "abc-nodes-docker-registry" {
  namespace = [[ var "services_namespace" . | quote ]]
  type      = "service"
  priority  = 80

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
        image = [[ var "docker_registry_image" . | quote ]]
      }

      env {
        REGISTRY_HTTP_ADDR                        = "0.0.0.0:5000"
        REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY = "/var/lib/registry"
        REGISTRY_HTTP_RELATIVEURLS                = "true"
        REGISTRY_STORAGE_DELETE_ENABLED           = "true"
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
