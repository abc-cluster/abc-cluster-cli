# DEPRECATED — superseded by vault/vault.service (systemd)
#
# Vault is now managed as a systemd service on aither, outside Nomad.
# This avoids the circular dependency (Vault scheduled by the thing that needs it)
# and ensures Vault starts before Nomad (Before=nomad.service in vault.service).
#
# Replacement files:
#   vault/vault.hcl                  — server config
#   vault/vault.service              — systemd unit
#   vault/deploy-vault.sh            — deploy script (mirrors deploy-consul.sh)
#
# To migrate from this Nomad job:
#   1. abc admin services nomad cli -- job stop -namespace=abc-services -purge abc-nodes-vault
#   2. bash deployments/abc-nodes/vault/deploy-vault.sh
#
# This file is kept for reference only. Do not re-deploy it.
# ──────────────────────────────────────────────────────────────────────────────
# HashiCorp Vault — abc-nodes floor (production)
#
# Graduated from experimental/nomad/vault.nomad.hcl.
# Changes from experimental:
#   • namespace = "abc-services"  (matches all production services)
#   • provider  = "consul"        (replaces deprecated nomad provider)
#   • constraint block            (pinned to aither)
#   • Traefik catalog tags        (vault.aither vhost via Traefik → Caddy)
#   • auto-unseal poststart task  (reads keys from Nomad variable abc-nodes/vault-unseal)
#
# Migration from experimental:
#   1.  nomad job stop -namespace=services abc-nodes-vault     # stop old job
#   2.  Run bootstrap-secrets.sh to populate Nomad variables
#   3.  nomad job run deployments/abc-nodes/nomad/vault.nomad.hcl
#       (Raft data at /opt/nomad/vault/data survives — existing secrets preserved)
#
# First deploy (fresh node, no existing Vault data):
#   1.  Run bootstrap-secrets.sh  (pre-populates KMS var + copies unseal keys later)
#   2.  Deploy this job
#   3.  Run vault/init-vault.sh   (initializes, saves keys, populates unseal var)
#   4.  The poststart unseal task will auto-unseal on every future restart
#
# SSH credential flow (once Vault is up):
#   Run vault/setup-ssh-ca.sh    (enables SSH secrets engine, creates roles)
#   Run vault/configure-node.sh  (trusts Vault CA on target SSH hosts)
#   Users: vault write ssh-client-signer/sign/ssh-role public_key=@~/.ssh/id_rsa.pub
#
# Access:
#   UI:  http://vault.aither/      (Caddy → Traefik → Vault:8200)
#   CLI: export VAULT_ADDR=http://vault.aither
#        vault login  (use root token from vault/acl/vault-keys.env)

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "vault_version" {
  type    = string
  default = "1.18.3"
}

# Host path for Raft data — survives Nomad restarts and alloc replacement.
# MUST match the path used during vault operator init.
variable "vault_data_dir" {
  type    = string
  default = "/opt/nomad/vault/data"
}

variable "vault_node_id" {
  type    = string
  default = "abc-nodes-vault-1"
}

job "abc-nodes-vault" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"
  priority    = 80  # Keep higher than user jobs; other services may depend on Vault.

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "vault"
  }

  # Pin to aither: Raft data lives on aither's host volume.
  constraint {
    attribute = "${attr.unique.hostname}"
    value     = "aither"
  }

  group "vault" {
    count = 1

    network {
      mode = "host"
      port "api"     { static = 8200 }
      port "cluster" { static = 8201 }
    }

    # ── Main Vault server task ────────────────────────────────────────────────
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
        provider = "consul"
        tags = [
          "abc-nodes", "vault", "secrets",
          "traefik.enable=true",
          "traefik.http.routers.vault.rule=Host(`vault.aither`)",
          "traefik.http.routers.vault.entrypoints=web",
          "traefik.http.services.vault.loadbalancer.server.port=8200",
        ]

        check {
          type     = "http"
          # standbyok   → 200 when in standby (instead of 429)
          # uninitcode  → 200 when uninitialized (instead of 501)
          # sealedcode  → 200 when sealed (instead of 503)
          path     = "/v1/sys/health?standbyok=true&uninitcode=200&sealedcode=200"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }

    # ── Auto-unseal task (runs once after Vault server starts) ────────────────
    #
    # Reads 3 unseal key shares from Nomad variable "abc-nodes/vault-unseal"
    # (populated by vault/bootstrap-secrets.sh). If Vault is already unsealed,
    # exits 0 immediately. On restart Vault re-seals; this task re-unseals it.
    #
    # Populate the variable:
    #   source deployments/abc-nodes/acl/vault-keys.env
    #   nomad var put -namespace=abc-services -force abc-nodes/vault-unseal \
    #     key_1="$VAULT_UNSEAL_KEY_1" key_2="$VAULT_UNSEAL_KEY_2" key_3="$VAULT_UNSEAL_KEY_3"
    task "unseal" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = false
      }

      config {
        command = "/bin/sh"
        args    = ["${NOMAD_TASK_DIR}/unseal.sh"]
      }

      template {
        data        = <<EOF
#!/bin/sh
set -e

VAULT_ADDR="http://127.0.0.1:8200"
export VAULT_ADDR

log() { echo "unseal: $*"; }

# Wait for Vault API to come up (up to 60 s)
for i in $(seq 1 30); do
  STATUS_CODE=$(curl -sf -o /dev/null -w '%%{http_code}' \
    "${VAULT_ADDR}/v1/sys/health?sealedcode=200&uninitcode=200&standbyok=true" 2>/dev/null || echo "000")
  [ "$STATUS_CODE" != "000" ] && break
  log "waiting for vault API... ($i/30)"
  sleep 2
done

if [ "$STATUS_CODE" = "000" ]; then
  log "ERROR: vault API did not come up in 60 s"
  exit 1
fi

# Check seal status
SEALED=$(curl -sf "${VAULT_ADDR}/v1/sys/seal-status" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['sealed'])" 2>/dev/null || echo "true")

if [ "$SEALED" = "False" ] || [ "$SEALED" = "false" ]; then
  log "vault is already unsealed — nothing to do"
  exit 0
fi

log "vault is sealed, unsealing with 3 key shares..."

unseal_key() {
  curl -sf -X PUT "${VAULT_ADDR}/v1/sys/unseal" \
    -H "Content-Type: application/json" \
    -d "{\"key\": \"$1\"}" \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print('  progress: '+str(d.get('progress',0))+'/'+str(d.get('t',3))+' sealed='+str(d.get('sealed','?')))"
}

unseal_key "{{ with nomadVar "nomad/jobs/abc-nodes-vault" }}{{ .key_1 }}{{ end }}"
unseal_key "{{ with nomadVar "nomad/jobs/abc-nodes-vault" }}{{ .key_2 }}{{ end }}"
unseal_key "{{ with nomadVar "nomad/jobs/abc-nodes-vault" }}{{ .key_3 }}{{ end }}"

log "done"
EOF
        destination = "local/unseal.sh"
      }

      resources {
        cpu    = 50
        memory = 64
      }
    }
  }
}
