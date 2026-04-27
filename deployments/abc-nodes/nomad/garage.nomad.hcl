# Garage S3-compatible storage (service) — abc-nodes floor
#
# ROLE IN THE CLUSTER
# ───────────────────
#  Long-term archive + cluster-backup tier behind RustFS.  Garage adds:
#   - block-level zstd compression (default level 1) — wins on text/JSON/VCF/snapshots
#   - content-addressed block dedup — wins on overlapping objects + reference data
#   - online-rebalanceable replication — replication_factor=1 today, upgrade to 3
#     across sites later with no data re-import
#
#  RustFS stays the hot tier (tusd uploads, ntfy attachments).  Garage is consumed
#  server-side from inside the cluster:
#   - restic-on-Garage for cluster snapshots (see abc-backups.nomad.hcl)
#   - fx-archive periodic tier-down from RustFS (see fx/fx-archive.nomad.hcl)
#
# DATA PERSISTENCE
# ────────────────
#  Metadata at /opt/nomad/scratch/garage-meta on aither (LMDB; mmap-friendly).
#  Object data at /opt/nomad/scratch/garage-data on aither.
#  Both are subdirs of the existing "scratch" host volume — survives node reboots.
#
# ENDPOINTS
# ─────────
#  S3 API   : http://100.70.185.46:3900   (or http://garage.aither/)
#  RPC      : 100.70.185.46:3901          (peer / cluster bus, internal)
#  Web      : http://100.70.185.46:3902   (static-site host, currently unused)
#  Admin    : http://100.70.185.46:3903   (admin API + Prometheus, internal)
#  WebUI    : http://garage-webui.aither/ (separate group; admin-API + bearer token)
#
# BOOTSTRAP
# ─────────
#  A poststart task runs `garage layout assign / apply`, creates buckets
#  (cluster-backups, archive, datasets), and imports deterministic S3 keys
#  (restic-key, archive-key) provided as job vars.  Idempotent — safe to re-run.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "garage_image" {
  type    = string
  default = "dxflrs/garage:v1.1.0"
}

# ── Garage cluster secrets ───────────────────────────────────────────────────
# rpc_secret authenticates peer-to-peer RPC. With one node it's only ever
# self-talk, but the field is required by Garage's config validator.
variable "garage_rpc_secret" {
  type        = string
  description = "Hex string (64 chars / 32 bytes) — `openssl rand -hex 32`"
  default     = "0000000000000000000000000000000000000000000000000000000000000000"
}

variable "garage_admin_token" {
  type        = string
  description = "Bearer token for the admin API (port 3903) — used by garage-webui"
  default     = "change-me-admin-token-please"
}

variable "garage_metrics_token" {
  type        = string
  description = "Bearer token for /metrics (Prometheus scrape) on the admin port"
  default     = "change-me-metrics-token-please"
}

# ── Deterministic S3 access keys (imported via `garage key import`) ──────────
# Garage normally generates random AK/SK on `key create`. To make them stable
# across deploys (and surface them via terraform vars instead of CLI exec
# round-trips), we import explicit AK/SK values via `garage key import`.

variable "garage_restic_access_key" {
  type        = string
  description = "AK for the restic-on-Garage backup repo (cluster-backups bucket)"
  default     = "GKADMIN000000000000RESTIC"
}

variable "garage_restic_secret_key" {
  type        = string
  default     = "change-me-restic-secret-key-32chars-min"
}

variable "garage_archive_access_key" {
  type        = string
  description = "AK for the fx-archive tier-down job (archive + datasets buckets)"
  default     = "GKADMIN00000000000ARCHIVE"
}

variable "garage_archive_secret_key" {
  type        = string
  default     = "change-me-archive-secret-key-32chars-min"
}

# ── Layout sizing ────────────────────────────────────────────────────────────
# Capacity advertised to Garage's layout planner. With replication_factor=1 on
# a single node this is mostly informational; matters when you grow the cluster.
variable "garage_node_capacity" {
  type    = string
  default = "100G"
}

variable "garage_zone" {
  type    = string
  default = "dc1"
}

# WebUI image — kept separate so it can lag behind the server.
variable "garage_webui_image" {
  type    = string
  default = "khairul169/garage-webui:latest"
}

job "abc-nodes-garage" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "garage"
  }

  # ════════════════════════════════════════════════════════════════════════════
  # Group 1: garage server + bootstrap (poststart)
  # ════════════════════════════════════════════════════════════════════════════
  group "garage" {
    count = 1

    # Pin to aither: data lives on aither's scratch host volume.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "s3" {
        static = 3900
        to     = 3900
      }
      port "rpc" {
        static = 3901
        to     = 3901
      }
      port "web" {
        static = 3902
        to     = 3902
      }
      port "admin" {
        static = 3903
        to     = 3903
      }
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # ── Ensure data + meta dirs exist with correct perms ────────────────────
    # Garage runs as root in the published image so this is mostly a "make sure
    # the dirs exist" step (the LMDB metadata file would fail on first start
    # if its directory doesn't exist).  0700 because metadata contains
    # cluster secrets after first startup.
    task "ensure-data-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args = [
          "mkdir -p /scratch/garage-meta /scratch/garage-data && chmod 0700 /scratch/garage-meta /scratch/garage-data",
        ]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }

    # ── Garage server ────────────────────────────────────────────────────────
    task "garage" {
      driver = "containerd-driver"

      config {
        image = var.garage_image
        # Default entrypoint is `/garage server`; explicit for clarity.
        entrypoint = ["/garage"]
        args       = ["server"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        # Tells the garage CLI (and server) where to find the config file.
        GARAGE_CONFIG_FILE = "/local/garage.toml"
      }

      template {
        destination = "local/garage.toml"
        change_mode = "restart"
        # Garage 1.x config — keys per
        # https://garagehq.deuxfleurs.fr/documentation/reference-manual/configuration/
        data = <<-EOF
metadata_dir = "/scratch/garage-meta"
data_dir     = "/scratch/garage-data"
db_engine    = "lmdb"

# Single-node deploy.  consistency_mode="dangerous" is required for RF=1 per
# the Garage docs (bypasses the quorum sanity check).  Online upgrade path to
# RF=3 later: add nodes via `garage node connect` + `garage layout assign` +
# bump replication_factor and `garage layout apply`.
replication_factor = 1
consistency_mode   = "dangerous"

rpc_bind_addr   = "[::]:3901"
rpc_public_addr = "100.70.185.46:3901"
rpc_secret      = "${var.garage_rpc_secret}"

# zstd compression at the block layer.  Default 1 is a solid compromise; raise
# to 6 if cold-archive CPU is cheap.  "none" disables globally (no per-bucket).
compression_level = 1

[s3_api]
api_bind_addr = "[::]:3900"
s3_region     = "garage"
root_domain   = ".garage.aither"

[s3_web]
bind_addr   = "[::]:3902"
root_domain = ".web.garage.aither"
index       = "index.html"

[admin]
api_bind_addr  = "[::]:3903"
admin_token    = "${var.garage_admin_token}"
metrics_token  = "${var.garage_metrics_token}"
EOF
      }

      resources {
        cpu    = 500
        memory = 512
      }

      service {
        name     = "abc-nodes-garage-s3"
        port     = "s3"
        provider = "consul"
        tags = [
          "abc-nodes", "garage", "s3",
          "traefik.enable=true",
          "traefik.http.routers.garage.rule=Host(`garage.aither`)",
          "traefik.http.routers.garage.entrypoints=web",
          "traefik.http.services.garage.loadbalancer.server.port=3900",
        ]

        # TCP check — Garage S3 root returns 403 (signature required) which is
        # not a clean health signal.  Admin port has /health but is on a
        # separate service entry below.
        check {
          name     = "garage-s3-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }

      service {
        name     = "abc-nodes-garage-admin"
        port     = "admin"
        provider = "consul"
        tags     = ["abc-nodes", "garage", "admin"]

        check {
          name     = "garage-admin-health"
          type     = "http"
          path     = "/health"
          interval = "15s"
          timeout  = "3s"
        }
      }

      service {
        name     = "abc-nodes-garage-rpc"
        port     = "rpc"
        provider = "consul"
        tags     = ["abc-nodes", "garage", "rpc"]

        check {
          name     = "garage-rpc-tcp"
          type     = "tcp"
          interval = "30s"
          timeout  = "3s"
        }
      }
    }

    # ── Bootstrap (idempotent) ──────────────────────────────────────────────
    # poststart with sidecar=false: runs ONCE after the main `garage` task is
    # started, then exits.  Re-running on subsequent deploys is safe — every
    # step is gated on a "does it already exist?" check.
    task "bootstrap" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "poststart"
        sidecar = false
      }

      config {
        image      = var.garage_image
        entrypoint = ["/bin/sh", "-c"]
        args       = ["${NOMAD_TASK_DIR}/bootstrap.sh"]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = true
      }

      env {
        GARAGE_CONFIG_FILE  = "/local/garage.toml"
        GARAGE_NODE_CAP     = var.garage_node_capacity
        GARAGE_ZONE         = var.garage_zone
        RESTIC_AK           = var.garage_restic_access_key
        RESTIC_SK           = var.garage_restic_secret_key
        ARCHIVE_AK          = var.garage_archive_access_key
        ARCHIVE_SK          = var.garage_archive_secret_key
      }

      # The bootstrap CLI shares the network namespace with the `garage` task
      # (same group, bridge mode) so it can reach the server on 127.0.0.1:3901.
      # The CLI only needs rpc_secret + a reachable rpc_public_addr from the
      # config; metadata_dir/data_dir are server-only fields and ignored by
      # client commands like `status`, `bucket`, `key`, `layout`.
      template {
        destination = "local/garage.toml"
        data        = <<-EOF
metadata_dir = "/scratch/garage-meta"
data_dir     = "/scratch/garage-data"
db_engine    = "lmdb"
replication_factor = 1
consistency_mode   = "dangerous"
rpc_bind_addr   = "127.0.0.1:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret      = "${var.garage_rpc_secret}"
compression_level = 1
[s3_api]
api_bind_addr = "127.0.0.1:3900"
s3_region     = "garage"
root_domain   = ".garage.aither"
[admin]
api_bind_addr = "127.0.0.1:3903"
admin_token   = "${var.garage_admin_token}"
metrics_token = "${var.garage_metrics_token}"
EOF
      }

      template {
        destination = "local/bootstrap.sh"
        perms       = "0755"
        data        = <<-SCRIPT
#!/bin/sh
# Idempotent Garage bootstrap.
# Runs as a `poststart sidecar=false` task — executes once after the server
# starts, then exits.
set -eu

CFG="$GARAGE_CONFIG_FILE"

# `garage` CLI talks to the running server over RPC using the config above.
# Wait for the server to be ready before issuing layout commands.
echo "[bootstrap] waiting for garage server RPC..."
for i in $(seq 1 30); do
  if /garage -c "$CFG" status >/dev/null 2>&1; then
    echo "[bootstrap] server is up (after $${i}s)"
    break
  fi
  sleep 1
done

# Layout: assign the local node a slot in zone $GARAGE_ZONE.
NODE_ID=$(/garage -c "$CFG" node id -q 2>/dev/null | head -c 64 | tr -d '\n' || true)
if [ -z "$NODE_ID" ]; then
  echo "[bootstrap] could not read node id; aborting"
  exit 1
fi
SHORT_ID=$(echo "$NODE_ID" | head -c 16)

if /garage -c "$CFG" layout show 2>/dev/null | grep -q "No nodes currently have a role"; then
  echo "[bootstrap] assigning layout role to $SHORT_ID (zone=$GARAGE_ZONE cap=$GARAGE_NODE_CAP)"
  /garage -c "$CFG" layout assign -z "$GARAGE_ZONE" -c "$GARAGE_NODE_CAP" -t aither "$SHORT_ID"
  /garage -c "$CFG" layout apply --version 1
else
  echo "[bootstrap] layout already configured; skipping assign"
fi

# Buckets — idempotent; create-if-missing.
for B in cluster-backups archive datasets; do
  if /garage -c "$CFG" bucket info "$B" >/dev/null 2>&1; then
    echo "[bootstrap] bucket $B already exists"
  else
    echo "[bootstrap] creating bucket $B"
    /garage -c "$CFG" bucket create "$B"
  fi
done

# Keys — `garage key import` lets us set deterministic AK/SK from terraform vars.
import_key() {
  NAME="$1"; AK="$2"; SK="$3"
  if /garage -c "$CFG" key info "$NAME" >/dev/null 2>&1; then
    echo "[bootstrap] key $NAME already imported"
  else
    echo "[bootstrap] importing key $NAME"
    /garage -c "$CFG" key import --yes --name "$NAME" "$AK" "$SK"
  fi
}
import_key restic-key  "$RESTIC_AK"  "$RESTIC_SK"
import_key archive-key "$ARCHIVE_AK" "$ARCHIVE_SK"

# Bucket permissions (idempotent — `bucket allow` is additive but re-issuing
# the same grant is a no-op).
/garage -c "$CFG" bucket allow --read --write --owner cluster-backups --key restic-key
/garage -c "$CFG" bucket allow --read --write --owner archive         --key archive-key
/garage -c "$CFG" bucket allow --read --write --owner datasets        --key archive-key

echo "[bootstrap] done"
SCRIPT
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }

  # ════════════════════════════════════════════════════════════════════════════
  # Group 2: garage-webui — admin UI (talks to admin API + S3 endpoint)
  # ════════════════════════════════════════════════════════════════════════════
  # Separate group so it doesn't share the `scratch` host volume mount and
  # can be restarted independently of the server.  No login-host coupling
  # (auth is a static bearer token against the admin API), so unlike RustFS
  # we can serve it from any *.aither host without surgery.
  group "garage-webui" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        to = 3909
      }
    }

    task "webui" {
      driver = "containerd-driver"

      config {
        image = var.garage_webui_image
      }

      env {
        # khairul169/garage-webui reads these envs at startup.
        API_BASE_URL    = "http://abc-nodes-garage-admin.service.consul:3903"
        S3_ENDPOINT_URL = "http://abc-nodes-garage-s3.service.consul:3900"
        S3_REGION       = "garage"
        # Admin token is forwarded by the UI to the admin API.
        API_ADMIN_KEY   = var.garage_admin_token
        PORT            = "3909"
      }

      resources {
        cpu    = 100
        memory = 128
      }

      service {
        name     = "abc-nodes-garage-webui"
        port     = "http"
        provider = "consul"
        tags = [
          "abc-nodes", "garage", "webui",
          "traefik.enable=true",
          "traefik.http.routers.garage-webui.rule=Host(`garage-webui.aither`)",
          "traefik.http.routers.garage-webui.entrypoints=web",
          "traefik.http.services.garage-webui.loadbalancer.server.port=3909",
        ]

        check {
          name     = "garage-webui-tcp"
          type     = "tcp"
          interval = "30s"
          timeout  = "3s"
        }
      }
    }
  }
}
