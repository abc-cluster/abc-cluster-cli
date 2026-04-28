# GitRiver — self-hosted Git platform (abc-experimental namespace)
#
# WHAT IT IS
# ──────────
#  Single Rust binary that bundles git hosting (HTTP smart + SSH), releases
#  with binary artifacts, container registry, and a web UI / setup wizard.
#  https://gitriver.com  ·  image: gitriver/gitriver:latest
#
# ROLE IN THE CLUSTER
# ───────────────────
#  Private project hosting + release / artifact distribution for the cluster:
#   - `git clone http://gitriver.aither/<org>/<repo>.git` from inside Nomad
#     allocs (e.g. as a prestart task that fetches workflow code).
#   - SSH push from operator laptops via Tailscale: `ssh -p 2222 git@aither`.
#   - Release artifacts uploaded via the GitRiver web UI / API token.
#   - Container registry endpoint for `docker push gitriver.aither/<org>/<image>`.
#
#  CI/build runner is intentionally NOT enabled — the upstream compose example
#  mounts /var/run/docker.sock which doesn't apply to our containerd-driver
#  setup.  Stick to repos + releases + registry.  Revisit CI once GitRiver
#  has a documented containerd / nomad executor.
#
# DATA PERSISTENCE
# ────────────────
#  /scratch/gitriver-app  → /var/lib/gitriver  (repos, registry blobs, etc.)
#  /scratch/gitriver-pg   → /var/lib/postgresql/data  (Postgres 17 cluster)
#
#  Both subdirs of the existing "scratch" host volume; survives reboots.
#  Two groups in one job: `postgres` and `server`.  Postgres registers in
#  Consul as `abc-experimental-gitriver-pg` and the server reaches it via
#  the Consul DNS name (no hard-coded IP).
#
# ENDPOINTS
# ─────────
#  Web        : http://gitriver.aither/                (Tailscale, via Caddy)
#  HTTP git   : http://gitriver.aither/<org>/<repo>.git
#  SSH git    : ssh://git@100.70.185.46:2222/<org>/<repo>.git  (Tailscale)
#  Postgres   : abc-experimental-gitriver-pg.service.consul:5433  (in-cluster)
#
# BOOTSTRAP (first start)
# ───────────────────────
#  GitRiver runs a setup wizard on first launch — no env-var bootstrap is
#  documented upstream.  After deploy:
#    1. Open http://gitriver.aither/ on a tailnet device.
#    2. Create the admin user via the wizard.
#    3. Note the admin credentials in the team password manager.
#    4. (Optional) generate an API token under Settings → Access Tokens for
#       use by Nomad job prestart tasks that `git clone` private repos.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "gitriver_image" {
  type        = string
  default     = "gitriver/gitriver:latest"
  description = "Pin to a specific tag once one is published; latest tracks main."
}

variable "gitriver_entrypoint_cmd" {
  type    = string
  default = <<-CMD
    set -e
    mkdir -p /scratch/gitriver-app /scratch/gitriver-app/repos
    # If the image ships bundled content in /var/lib/gitriver and our persisted
    # dir is still empty, seed it once.  After this, /var/lib/gitriver is a
    # symlink to the persisted dir.
    if [ -d /var/lib/gitriver ] && [ ! -L /var/lib/gitriver ]; then
      if [ -z "$(ls -A /scratch/gitriver-app 2>/dev/null)" ] && [ -n "$(ls -A /var/lib/gitriver 2>/dev/null)" ]; then
        cp -a /var/lib/gitriver/. /scratch/gitriver-app/
      fi
      rm -rf /var/lib/gitriver
    fi
    ln -sfn /scratch/gitriver-app /var/lib/gitriver
    exec gitriver run --config /local/gitriver.toml
  CMD
  description = "Shell command — seeds + symlinks /var/lib/gitriver to /scratch, then execs `gitriver run` with the rendered TOML config."
}

variable "postgres_image" {
  type    = string
  default = "postgres:17-alpine"
}

variable "gitriver_db_user" {
  type    = string
  default = "gitriver"
}

variable "gitriver_db_password" {
  type        = string
  default     = "change-me-via-tfvars"
  description = "DB password — terraform random_password supplies this in production."
}

variable "gitriver_db_name" {
  type    = string
  default = "gitriver"
}

variable "gitriver_base_url" {
  type        = string
  default     = "http://gitriver.aither"
  description = "External URL of the instance — used for email links, CI vars, webhooks."
}

variable "gitriver_http_port" {
  type        = number
  default     = 3030
  description = "Static host port for the GitRiver HTTP listener. NOT 3000 — that's Grafana."
}

# GitRiver's internal SSH port is undocumented but most Rust SSH servers in
# containers default to a non-privileged port.  We assume 22 inside; if the
# binary actually listens elsewhere, adjust `to` after observing logs.
variable "gitriver_ssh_internal_port" {
  type    = number
  default = 22
}

variable "gitriver_ssh_host_port" {
  type        = number
  default     = 2222
  description = "Static host port for git-over-SSH — clients use ssh://git@<aither>:<this>/."
}

variable "postgres_static_port" {
  type        = number
  default     = 5433
  description = "Host port for GitRiver's dedicated Postgres — distinct from the experimental shared 5432."
}

variable "db_host_ip" {
  type        = string
  default     = "100.70.185.46"
  description = "IP the gitriver server uses to reach Postgres. Tailscale IP works because gitriver runs in bridge mode where Consul DNS is not wired up; both groups are pinned to aither so this is stable. Pattern matches tusd's S3 endpoint."
}

job "abc-experimental-gitriver" {
  namespace   = "abc-experimental"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"
  priority    = 60

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "gitriver"
  }

  # ════════════════════════════════════════════════════════════════════════════
  # Group 1: Postgres (dedicated to GitRiver — not shared with supabase/wave)
  # ════════════════════════════════════════════════════════════════════════════
  group "postgres" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "pg" {
        static = var.postgres_static_port
        to     = 5432
      }
    }

    restart {
      attempts = 3
      delay    = "15s"
      interval = "1m"
      mode     = "delay"
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # Pre-create the PGDATA dir at correct perms before postgres starts.
    task "ensure-pg-dir" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args = [
          "mkdir -p /scratch/gitriver-pg && chown -R 70:70 /scratch/gitriver-pg && chmod 0700 /scratch/gitriver-pg",
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

    task "postgres" {
      driver = "containerd-driver"

      config {
        image = var.postgres_image
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        POSTGRES_DB       = var.gitriver_db_name
        POSTGRES_USER     = var.gitriver_db_user
        POSTGRES_PASSWORD = var.gitriver_db_password
        PGDATA            = "/scratch/gitriver-pg"
      }

      resources {
        cpu    = 300
        memory = 512
      }

      service {
        name     = "abc-experimental-gitriver-pg"
        port     = "pg"
        provider = "consul"
        tags     = ["abc-experimental", "gitriver", "postgres"]

        check {
          name     = "gitriver-pg-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }

  # ════════════════════════════════════════════════════════════════════════════
  # Group 2: GitRiver server
  # ════════════════════════════════════════════════════════════════════════════
  group "server" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = var.gitriver_http_port
        to     = 8080
      }
      # NOTE: GitRiver SSH integrates via the host's OpenSSH + authorized_keys
      # (`gitriver serv` invoked through ForceCommand), not a built-in SSH
      # listener.  We skip exposing an SSH port here; HTTP push/pull works
      # for the day-one "pull repos in Nomad job defs" use case.  Wire SSH
      # later by mounting the host's authorized_keys path or running
      # `gitriver serv` from a separate task on the host.
    }

    restart {
      attempts = 3
      delay    = "20s"
      interval = "5m"
      mode     = "delay"
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # Pre-create the GitRiver app data dir.
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
          "mkdir -p /scratch/gitriver-app && chmod 0755 /scratch/gitriver-app",
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

    task "gitriver" {
      driver = "containerd-driver"

      # Override entrypoint to symlink /var/lib/gitriver → /scratch/gitriver-app
      # before the binary starts.  GitRiver writes to /var/lib/gitriver per the
      # upstream compose example and there's no documented env var to
      # redirect the data dir; we can't bind-mount our shared scratch volume
      # at /var/lib (it would shadow other things) and adding a dedicated
      # host_volume requires editing Nomad client config on aither.  The
      # symlink approach gives us persistence on the existing scratch volume
      # with zero out-of-band setup.
      #
      # `exec gitriver` assumes the binary is on PATH — true for the published
      # gitriver/gitriver image (Rust single-binary entrypoint).  If a future
      # tag changes the binary location, override `gitriver_entrypoint_cmd`.
      config {
        image      = var.gitriver_image
        entrypoint = ["/bin/sh", "-c"]
        args       = [var.gitriver_entrypoint_cmd]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      # GitRiver reads its full config from TOML (env vars aren't honored by
      # `gitriver run`).  Nomad's template renders Consul-resolved values into
      # /local/gitriver.toml on alloc start.
      template {
        destination = "local/gitriver.toml"
        change_mode = "restart"
        data        = <<-EOT
host = "0.0.0.0"
port = 8080
base_url = "${var.gitriver_base_url}"

# Postgres DSN — direct host:port (Tailscale IP) because Consul DNS is not
# wired into bridge-mode containers' resolv.conf in this cluster.  Both
# groups pinned to aither so the host IP is stable.
database_url = "postgres://${var.gitriver_db_user}:${var.gitriver_db_password}@${var.db_host_ip}:${var.postgres_static_port}/${var.gitriver_db_name}"

# Bare repos persisted on the scratch host volume via the symlink set up by
# the entrypoint (see gitriver_entrypoint_cmd).
git_repos_path = "/var/lib/gitriver/repos"

# SPA bundle lives in the image at /usr/share/gitriver/web (NOT under the
# persisted data dir).  The default of /var/lib/gitriver/web would point at
# our symlinked persistent dir which doesn't contain the SPA, causing 404 on
# every web route (/login, etc.).  Pin to the image-baked path.
web_dist_path = "/usr/share/gitriver/web"

# JWT secret persists at /var/lib/gitriver/.jwt_secret on first start; lives
# on the scratch volume thanks to the symlink, so it survives job restarts.

# Container Registry storage — defaults to filesystem under
# /var/lib/gitriver/registry.  To swap to RustFS / Garage as the registry
# backend later, uncomment the [s3] section and create the bucket.
#
# [s3]
# endpoint   = "http://abc-nodes-rustfs-s3.service.consul:9900"
# bucket     = "gitriver-registry"
# region     = "us-east-1"
# access_key = "rustfsadmin"
# secret_key = "rustfsadmin"
EOT
      }

      resources {
        cpu    = 1000
        memory = 768
      }

      service {
        name         = "abc-experimental-gitriver"
        port         = "http"
        provider     = "consul"
        # In bridge mode without address_mode=host, Nomad registers the
        # service against the alloc's IPv6 loopback (::1), which makes the
        # HTTP health check unreachable from the Nomad agent and breaks
        # Traefik's Consul-catalog discovery.  "host" forces the static
        # port mapping (host:3000) — same address Caddy already targets.
        address_mode = "host"
        tags = [
          "abc-experimental", "gitriver", "git-host",
        ]

        check {
          name     = "gitriver-http"
          type     = "http"
          # Root 302→/login; /login returns 200 (what Nomad HTTP check needs).
          path     = "/login"
          interval = "20s"
          timeout  = "5s"
        }
      }

      # SSH service deliberately not registered — see network{} note above.
    }
  }
}
