# HashiCorp Vault — abc-nodes floor (Raft integrated storage)
#
# Persists data to /opt/nomad/vault/data on the host via Raft integrated storage.
# Uses raw_exec so the process has full access to host paths and ports.
# https://developer.hashicorp.com/vault/docs/configuration/storage/raft
#
# First-run initialization (once, after the job is running):
#   export VAULT_ADDR=http://<node-ip>:8200
#   # Optional: when Caddy exposes /services/vault/ (see experimental/README.md):
#   # export VAULT_ADDR=http://aither.mb.sun.ac.za/services/vault
#   vault operator init           # prints 5 unseal keys + root token — store securely
#   vault operator unseal         # run 3× with different key shares
#   vault operator unseal
#   vault operator unseal
#
# Enable KV v2 for abc secrets (using root token):
#   export VAULT_TOKEN=<root-token>
#   vault secrets enable -path=secret kv-v2
#
# Wire into abc config (direct to node:8200, or via LAN gateway if you restore Caddy /services/vault/):
#   abc config set admin.services.vault.http "http://<node-ip>:8200"
#   abc config set admin.services.vault.http "http://aither.mb.sun.ac.za/services/vault"
#   abc cluster capabilities sync
#
# Admin — store / rotate a secret:
#   abc secrets set my-key "s3cr3t" --backend vault
#
# Job template reference (non-admin safe, no plaintext exposure):
#   abc secrets ref my-key --backend vault

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "vault_version" {
  type    = string
  default = "1.18.3"
}

# Host path for Raft data — survives Nomad restarts and alloc replacement.
variable "vault_data_dir" {
  type    = string
  default = "/opt/nomad/vault/data"
}

variable "vault_node_id" {
  type    = string
  default = "abc-nodes-vault-1"
}

job "abc-nodes-vault" {
  namespace = "services"
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
      mode = "host"
      port "api" {
        static = 8200
      }
      port "cluster" {
        static = 8201
      }
    }

    task "vault" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args = [
          "-c",
          "chmod +x ${NOMAD_TASK_DIR}/vault && mkdir -p ${var.vault_data_dir} && exec ${NOMAD_TASK_DIR}/vault server -config=${NOMAD_TASK_DIR}/vault.hcl",
        ]
      }

      artifact {
        source      = "https://releases.hashicorp.com/vault/${var.vault_version}/vault_${var.vault_version}_linux_amd64.zip"
        destination = "local/"
      }

      # Vault static configuration.
      # ${var.*} is resolved by HCL at job-parse time.
      # {{ env "..." }} is resolved by the Nomad template engine at alloc start.
      template {
        data        = <<EOF
disable_mlock = true
ui            = true

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 1
}

storage "raft" {
  path    = "${var.vault_data_dir}"
  node_id = "${var.vault_node_id}"
}

api_addr     = "http://{{ env "NOMAD_IP_api" }}:{{ env "NOMAD_PORT_api" }}"
cluster_addr = "http://{{ env "NOMAD_IP_cluster" }}:{{ env "NOMAD_PORT_cluster" }}"
EOF
        destination = "local/vault.hcl"
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-vault"
        port     = "api"
        provider = "nomad"
        tags     = ["abc-nodes", "vault", "secrets"]

        check {
          type     = "http"
          # standbyok   → return 200 when in standby (instead of 429)
          # uninitcode  → return 200 when uninitialized (instead of 501)
          # sealedcode  → return 200 when sealed (instead of 503)
          path     = "/v1/sys/health?standbyok=true&uninitcode=200&sealedcode=200"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
