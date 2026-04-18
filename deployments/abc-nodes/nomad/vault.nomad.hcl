# HashiCorp Vault — abc-nodes floor (lab **-dev** mode only)
#
# In-memory storage; data is lost on restart. For production use HashiCorp’s
# reference architecture (HA, raft/integrated storage, auto-unseal), not this job.
#
# After `job run`, set the same root token in ~/.abc, e.g.:
#   abc config set contexts.<name>.admin.services.vault.access_key '<dev-root-token>'

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "vault_image" {
  type    = string
  default = "hashicorp/vault:1.18.3"
}

variable "vault_dev_root_token_id" {
  type        = string
  description = "Root token ID for -dev (lab only; rotate for anything serious)."
  default     = "dev-root-token"
}

job "abc-nodes-vault" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "vault"
  }

  group "vault" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 8200
        to     = 8200
      }
    }

    task "vault" {
      driver = "containerd-driver"

      config {
        image = var.vault_image
        args  = ["server", "-dev"]
      }

      env {
        VAULT_DEV_ROOT_TOKEN_ID  = var.vault_dev_root_token_id
        VAULT_DEV_LISTEN_ADDRESS = "0.0.0.0:8200"
        VAULT_API_ADDR           = "http://0.0.0.0:8200"
        VAULT_LOG_LEVEL          = "INFO"
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-vault"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "vault", "secrets",
          "traefik.enable=true",
          "traefik.http.routers.vault.rule=Host(`vault.aither`)",
          "traefik.http.services.vault.loadbalancer.server.port=8200",
        ]
      }
    }
  }
}
