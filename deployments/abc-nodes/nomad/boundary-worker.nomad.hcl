# HashiCorp Boundary Worker — abc-nodes floor (system job)
#
# Runs on EVERY node in the cluster (Nomad job type = system).
# Each worker proxies SSH sessions from Boundary clients to local services.
#
# Architecture
# ────────────
#  Client browser/CLI  →  Boundary controller (port 9200 API, 9202 data)
#                       →  Worker on target node (port 9202 proxy)
#                       →  SSH daemon on target node (port 22)
#
#  Workers authenticate to the controller using the shared worker-auth KMS key
#  (same AEAD key used in boundary-controller.nomad.hcl).
#
#  Worker state (authentication token) is persisted on the host at
#  /opt/nomad/boundary/worker-<hostname> so workers survive restarts.
#
# Prerequisites
# ─────────────
#  boundary-controller must be running and the db-init poststart task must
#  have completed before workers will successfully connect.
#
#  Nomad variable "abc-nodes/boundary-kms" must be populated
#  (same variable as boundary-controller.nomad.hcl).
#
# Deploy
# ──────
#  abc admin services nomad cli -- job run deployments/abc-nodes/nomad/boundary-worker.nomad.hcl
#
# Verify
# ──────
#  In the Boundary UI at http://boundary.aither, go to Workers to see all connected workers.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default", "sun-nomadlab", "oci-nomadlab"]
}

variable "boundary_version" {
  type    = string
  default = "0.18.2"
}

job "abc-nodes-boundary-worker" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "system"  # Runs on every node automatically

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "boundary-worker"
  }

  group "worker" {
    network {
      mode = "host"
      port "proxy" { static = 9203 }  # per-worker proxy port (avoids conflict with controller's 9202)
    }

    task "worker" {
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
exec "${NOMAD_TASK_DIR}/boundary" server -config="${NOMAD_TASK_DIR}/worker.hcl"
EOF
        destination = "local/start.sh"
      }

      template {
        data        = <<EOF
disable_mlock = true

listener "tcp" {
  address     = "0.0.0.0:9203"
  purpose     = "proxy"
  tls_disable = true
}

worker {
  # Unique name per node using the Nomad node unique name.
  name = "abc-nodes-worker-{{ env "node.unique.name" }}"

  # public_addr: the address Boundary tells clients to proxy SSH sessions through.
  # CRITICAL: must be a real routable IP, not 0.0.0.0 — otherwise boundary connect ssh hangs.
  # All cluster nodes are on Tailscale (100.x.y.z range); use that IP.
  # attr.unique.advertise.address contains "IP:port" — split on ":" and take first segment.
  public_addr = "{{ env "attr.unique.advertise.address" | regexReplaceAll ":.*$" "" }}:9203"

  # Controller address: direct Tailscale IP of aither (where boundary-controller runs).
  # Using direct IP because sun-nomadlab nodes do not run Consul clients;
  # Consul DNS (abc-nodes-boundary-cluster.service.consul) is only resolvable on aither.
  initial_upstreams = ["100.70.185.46:9201"]

  # NOTE: auth_storage_path is for PKI (file-based) worker auth only.
  # When using KMS-based auth (worker-auth kms block below), auth_storage_path
  # must NOT be set — the authentication is handled in-memory via the shared KMS key.

  tags {
    type   = ["abc-nodes-worker"]
    region = ["{{ env "node.datacenter" }}"]
  }
}

# Worker-auth KMS — must match the controller's worker-auth key
# (same key in /etc/boundary.d/controller.hcl on aither).
# KMS auth is mutually exclusive with auth_storage_path (PKI auth).
kms "aead" {
  purpose   = "worker-auth"
  aead_type = "aes-gcm"
  key       = "5EIEUcrJlrPauiiDWMUX7qiH53e6CTcSKcQo5iSDJ/c="
  key_id    = "global_worker_auth"
}
EOF
        destination = "local/worker.hcl"
      }

      resources {
        cpu    = 128
        memory = 256
      }

      service {
        name     = "abc-nodes-boundary-worker"
        port     = "proxy"
        # Use Nomad's built-in service discovery so this job can run on nodes
        # that do NOT have a Consul client (e.g. sun-nomadlab nodes).
        # Consul provider auto-injects a "${attr.consul.version} >= 1.8.0" constraint
        # which prevents placement on non-Consul nodes.
        provider = "nomad"
        tags     = ["abc-nodes", "boundary", "worker"]

        check {
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}
