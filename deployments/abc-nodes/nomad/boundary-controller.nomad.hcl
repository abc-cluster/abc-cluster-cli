# DEPRECATED — superseded by boundary/boundary-controller.service (systemd)
#
# The Boundary controller is now managed as a systemd service on aither, outside Nomad.
# This eliminates lifecycle coupling to Nomad scheduling and lets the controller
# start alongside Consul/Vault in the correct boot order.
#
# Replacement files:
#   boundary/boundary-controller.hcl        — config reference
#   boundary/boundary-controller.service    — systemd unit
#   boundary/deploy-boundary-controller.sh  — deploy script
#
# boundary-worker.nomad.hcl is KEPT as a Nomad system job — that is the correct
# pattern for a per-node proxy agent that auto-deploys to new cluster nodes.
#
# To migrate from this Nomad job:
#   1. abc admin services nomad cli -- job stop -namespace=abc-services -purge abc-nodes-boundary-controller
#   2. bash deployments/abc-nodes/boundary/deploy-boundary-controller.sh
#
# This file is kept for reference only. Do not re-deploy it.
# ──────────────────────────────────────────────────────────────────────────────
# HashiCorp Boundary Controller — abc-nodes floor
#
# Role in the SSH access stack
# ────────────────────────────
#  User → Boundary (session broker) → Worker on target node → SSH daemon
#  Vault provides SSH credentials (signed certificates) that Boundary uses
#  as a credential library so users never see raw private keys.
#
# Architecture
# ────────────
#  boundary-controller  (this job)  — service job, count=1, pinned to aither
#    • PostgreSQL backend for durable state (abc-nodes-postgres.service.consul:5432)
#    • AEAD KMS keys from Nomad variable "abc-nodes/boundary-kms"
#    • Ports: 9200 (API/UI), 9201 (cluster ← workers connect here), 9202 (data)
#
#  boundary-worker (system job)     — runs on EVERY node
#    • Connects to this controller on port 9201 via Consul DNS
#    • Proxies SSH sessions to local services
#
# Prerequisites
# ─────────────
#  1. abc-nodes-postgres must be running (job run postgres.nomad.hcl)
#  2. Run vault/bootstrap-secrets.sh to populate Nomad variable abc-nodes/boundary-kms
#  3. postgresql-client (psql) available on aither host (apt-get install postgresql-client)
#     The prestart task uses psql to create the boundary database if it does not exist.
#
# First-run setup (after the job is Running)
# ──────────────────────────────────────────
#  The poststart init task runs `boundary database init` automatically.
#  On first run it creates all schema tables and prints an initial auth method
#  password:
#
#    nomad alloc logs -namespace=abc-services <alloc-id> init
#
#  Save the "Initial login name" and "Initial login password" — they are only
#  printed once. Use them to log in at http://boundary.aither.
#
# Access
# ──────
#  UI:   http://boundary.aither/
#  CLI:  export BOUNDARY_ADDR=http://boundary.aither
#        boundary authenticate password -auth-method-id=<id> -login-name=admin
#
# SSH flow (after Boundary + Vault are configured)
# ─────────────────────────────────────────────────
#  See vault/setup-ssh-ca.sh for Vault SSH CA setup.
#  See boundary/setup-boundary.sh for target registration.
#
# Deploy
# ──────
#  abc admin services nomad cli -- job run deployments/abc-nodes/nomad/boundary-controller.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "boundary_version" {
  type    = string
  default = "0.18.2"
}

variable "db_host" {
  type    = string
  # Postgres has no Consul service registration — use direct Tailscale IP.
  # (abc-nodes-postgres is pinned to aither at 100.70.185.46)
  default = "100.70.185.46"
}

variable "db_port" {
  type    = string
  default = "5432"
}

variable "db_user" {
  type    = string
  default = "wave"
}

variable "db_password" {
  type    = string
  default = "wave_db_secret"
}

variable "db_name" {
  type    = string
  default = "boundary"
}

job "abc-nodes-boundary-controller" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "boundary-controller"
  }

  # Pin to aither: controller manages session state and must be stable.
  constraint {
    attribute = "${attr.unique.hostname}"
    value     = "aither"
  }

  group "controller" {
    count = 1

    network {
      mode = "host"
      port "api"     { static = 9200 }
      port "cluster" { static = 9201 }
      port "data"    { static = 9202 }
    }

    # ── Prestart: ensure the boundary database exists ────────────────────────
    # Installs postgresql-client if not present (first run), then creates
    # the boundary database idempotently. Subsequent runs are fast (apt-get
    # returns immediately when the package is already installed).
    task "create-db" {
      driver = "raw_exec"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        command = "/bin/sh"
        args    = ["${NOMAD_TASK_DIR}/create-db.sh"]
      }

      template {
        data        = <<EOF
#!/bin/sh
set -e

# Ensure psql is available
if ! command -v psql >/dev/null 2>&1; then
  echo "create-db: installing postgresql-client..."
  apt-get install -y -q postgresql-client 2>&1 | tail -3
fi

DB_HOST="${var.db_host}"
DB_PORT="${var.db_port}"
DB_USER="${var.db_user}"
DB_PASS="${var.db_password}"
DB_NAME="${var.db_name}"

export PGPASSWORD="$DB_PASS"

echo "create-db: checking if database '$DB_NAME' exists..."
EXISTS=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
  -tAc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'" 2>/dev/null || echo "")

if [ "$EXISTS" = "1" ]; then
  echo "create-db: database '$DB_NAME' already exists."
else
  echo "create-db: creating database '$DB_NAME'..."
  psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
    -c "CREATE DATABASE $DB_NAME;"
  echo "create-db: done."
fi
EOF
        destination = "local/create-db.sh"
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Main Boundary controller task ────────────────────────────────────────
    task "controller" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args    = ["${NOMAD_TASK_DIR}/start.sh"]
      }

      artifact {
        source      = "https://releases.hashicorp.com/boundary/${var.boundary_version}/boundary_${var.boundary_version}_linux_amd64.zip"
        destination = "local/"
      }

      template {
        data        = <<EOF
#!/bin/sh
set -e
chmod +x "${NOMAD_TASK_DIR}/boundary"
exec "${NOMAD_TASK_DIR}/boundary" server -config="${NOMAD_TASK_DIR}/controller.hcl"
EOF
        destination = "local/start.sh"
      }

      # Boundary controller configuration.
      # KMS keys are read from Nomad variable "abc-nodes/boundary-kms".
      # Populate with:
      #   vault/bootstrap-secrets.sh  (or run nomad var put manually)
      template {
        data        = <<EOF
disable_mlock = true

controller {
  name        = "abc-nodes-boundary-controller"
  description = "abc-nodes Boundary controller — manages SSH session brokering"

  database {
    url = "postgresql://{{ env "BOUNDARY_DB_USER" }}:{{ env "BOUNDARY_DB_PASSWORD" }}@{{ env "BOUNDARY_DB_HOST" }}:{{ env "BOUNDARY_DB_PORT" }}/{{ env "BOUNDARY_DB_NAME" }}?sslmode=disable"
  }
}

listener "tcp" {
  address              = "0.0.0.0:9200"
  purpose              = "api"
  tls_disable          = true
}

listener "tcp" {
  address              = "0.0.0.0:9201"
  purpose              = "cluster"
  tls_disable          = true
}

listener "tcp" {
  address              = "0.0.0.0:9202"
  purpose              = "proxy"
  tls_disable          = true
}

# Root KMS — encrypts Boundary's internal root key hierarchy.
kms "aead" {
  purpose   = "root"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .root_key }}{{ end }}"
  key_id    = "global_root"
}

# Worker-auth KMS — authenticates workers to this controller.
# The boundary-worker job uses the same key.
kms "aead" {
  purpose   = "worker-auth"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .worker_auth_key }}{{ end }}"
  key_id    = "global_worker_auth"
}

# Recovery KMS — used if root KMS is unavailable.
kms "aead" {
  purpose   = "recovery"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .recovery_key }}{{ end }}"
  key_id    = "global_recovery"
}
EOF
        destination = "local/controller.hcl"
      }

      env {
        BOUNDARY_DB_HOST     = "${var.db_host}"
        BOUNDARY_DB_PORT     = "${var.db_port}"
        BOUNDARY_DB_USER     = "${var.db_user}"
        BOUNDARY_DB_PASSWORD = "${var.db_password}"
        BOUNDARY_DB_NAME     = "${var.db_name}"
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-boundary-controller"
        port     = "api"
        provider = "consul"
        tags = [
          "abc-nodes", "boundary", "controller",
          "traefik.enable=true",
          "traefik.http.routers.boundary.rule=Host(`boundary.aither`)",
          "traefik.http.routers.boundary.entrypoints=web",
          "traefik.http.services.boundary.loadbalancer.server.port=9200",
        ]

        check {
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }

      # Cluster port — workers connect here. Registered separately in Consul
      # so boundary-worker.nomad.hcl can resolve it via service DNS.
      service {
        name     = "abc-nodes-boundary-cluster"
        port     = "cluster"
        provider = "consul"
        tags     = ["abc-nodes", "boundary", "cluster"]

        check {
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }

    # ── Poststart: initialize the database schema (idempotent) ───────────────
    #
    # On first deploy this creates all schema tables and prints initial
    # admin credentials. On subsequent deploys it is a no-op (schema exists).
    # Credentials are only printed once — capture them from alloc logs:
    #   nomad alloc logs -namespace=abc-services <alloc-id> db-init
    task "db-init" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = false
      }

      config {
        command = "/bin/sh"
        args    = ["${NOMAD_TASK_DIR}/db-init.sh"]
      }

      artifact {
        source      = "https://releases.hashicorp.com/boundary/${var.boundary_version}/boundary_${var.boundary_version}_linux_amd64.zip"
        destination = "local/"
      }

      template {
        data        = <<EOF
#!/bin/sh
set -e
chmod +x "${NOMAD_TASK_DIR}/boundary"

# Wait for controller API (up to 60 s)
for i in $(seq 1 30); do
  STATUS=$(curl -sf -o /dev/null -w '%%{http_code}' http://127.0.0.1:9200/v1/health 2>/dev/null || echo "000")
  [ "$STATUS" != "000" ] && break
  echo "db-init: waiting for controller API... ($i/30)"
  sleep 2
done

echo "db-init: running boundary database init (safe to re-run)..."
# Exit code 0 on success; non-zero if schema is already at current version (also safe).
"${NOMAD_TASK_DIR}/boundary" database init \
  -config="${NOMAD_TASK_DIR}/controller.hcl" \
  2>&1 | tee /dev/stdout || echo "db-init: schema already initialized or migration not needed"
EOF
        destination = "local/db-init.sh"
      }

      template {
        data        = <<EOF
disable_mlock = true

controller {
  name = "abc-nodes-boundary-controller"
  database {
    url = "postgresql://{{ env "BOUNDARY_DB_USER" }}:{{ env "BOUNDARY_DB_PASSWORD" }}@{{ env "BOUNDARY_DB_HOST" }}:{{ env "BOUNDARY_DB_PORT" }}/{{ env "BOUNDARY_DB_NAME" }}?sslmode=disable"
  }
}

kms "aead" {
  purpose   = "root"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .root_key }}{{ end }}"
  key_id    = "global_root"
}

kms "aead" {
  purpose   = "worker-auth"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .worker_auth_key }}{{ end }}"
  key_id    = "global_worker_auth"
}

kms "aead" {
  purpose   = "recovery"
  aead_type = "aes-gcm"
  key       = "{{ with nomadVar "nomad/jobs/abc-nodes-boundary-controller" }}{{ .recovery_key }}{{ end }}"
  key_id    = "global_recovery"
}
EOF
        destination = "local/controller.hcl"
      }

      env {
        BOUNDARY_DB_HOST     = "${var.db_host}"
        BOUNDARY_DB_PORT     = "${var.db_port}"
        BOUNDARY_DB_USER     = "${var.db_user}"
        BOUNDARY_DB_PASSWORD = "${var.db_password}"
        BOUNDARY_DB_NAME     = "${var.db_name}"
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }
}
