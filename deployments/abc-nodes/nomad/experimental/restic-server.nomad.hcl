# DEPRECATED — superseded by abc-backups.nomad.hcl (restic-on-Garage).
#
# This experimental restic REST server stores its repo on the local `scratch`
# host volume with no compression and no replication.  The same data now flows
# into Garage's `cluster-backups` bucket via the abc-backups periodic job, with
# encryption (restic), block-level dedup + zstd (Garage), and a future
# geo-replication path.  Keep this file in the repo as a reference; deploys
# default to disabled (var.enable_restic_server = false).
#
# Original header follows.
#
# Restic REST server — backup target (abc-experimental namespace)
# Provides an HTTP endpoint for `restic backup --repo rest:http://...` from
# any node or workstation that can reach aither.
#
# Authentication: htpasswd basic auth (user: abc, password configured at deploy time).
# Data stored on the scratch host volume at /scratch/restic-repo.
#
# Enable via Terraform:
#   terraform apply -var enable_restic_server=true
#
# Traefik route: http://restic.aither:8000  (or direct via static port)
#
# Usage:
#   export RESTIC_REPOSITORY=rest:http://abc:<password>@aither.mb.sun.ac.za:8000
#   export RESTIC_PASSWORD=<your-repo-encryption-passphrase>
#   restic init
#   restic backup /path/to/data

job "abc-experimental-restic-server" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 50

  group "restic-server" {
    count = 1

    # Pin to aither: restic data lives on aither's scratch host volume.
    # Rescheduling to another node would lose access to existing backup data.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "http" {
        static = 8000
        to     = 8000
      }
    }

    restart {
      attempts = 3
      delay    = "15s"
      interval = "2m"
      mode     = "delay"
    }

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    task "restic-server" {
      driver = "containerd-driver"

      config {
        image = "restic/rest-server:latest"
        args  = [
          "--listen", "0.0.0.0:8000",
          "--path", "/data",
          "--htpasswd-file", "/run/secrets/htpasswd",
          "--log-level", "info",
        ]
      }

      volume_mount {
        volume      = "scratch"
        destination = "/data"
        read_only   = false
      }

      # htpasswd file injected as a Nomad template.
      # Generate a new entry with:
      #   htpasswd -nB abc    (bcrypt)
      # or for quick dev setup:
      #   htpasswd -nb abc restic_secret   (MD5 — less secure)
      #
      # TODO: move the password hash into a Nomad Variable once WI JWT
      # verification is fixed on this cluster (currently returns 500).
      # For now the hash is inlined here. The default hash below corresponds
      # to password "restic_secret" (bcrypt rounds=10).
      template {
        data        = "abc:$2y$10$UwAIqgfTb7jFiDrSrOD.9.WQVPIAhv5u5OwQ2qLJ5Bh3lcpU5KsKq\n"
        destination = "/run/secrets/htpasswd"
        change_mode = "restart"
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
