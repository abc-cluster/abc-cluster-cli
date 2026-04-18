# ntfy (push notifications) — abc-nodes floor
# Default: cache under /var/cache/ntfy in the container (ephemeral).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "ntfy_image" {
  type    = string
  default = "binwiederhier/ntfy:v2.11.0"
}

variable "ntfy_base_url" {
  type        = string
  description = "Public URL users reach for the ntfy web UI / subscribe (used in server config)."
  default     = "http://127.0.0.1:8088"
}

job "abc-nodes-ntfy" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "ntfy"
  }

  group "ntfy" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        to = 80
      }
    }

    task "ntfy" {
      driver = "containerd-driver"

      config {
        image   = var.ntfy_image
        command = "ntfy"
        args    = ["serve", "--cache-file", "/var/cache/ntfy/cache.db"]
      }

      env {
        NTFY_BASE_URL         = var.ntfy_base_url
        NTFY_BEHIND_PROXY     = "true"
        NTFY_ATTACHMENT_CACHE_DIR = "/var/cache/ntfy/attachments"
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-ntfy"
        port     = "http"
        provider = "nomad"
        tags     = ["abc-nodes", "ntfy", "notifications"]
      }
    }
  }
}
