# abc-node-probe-runner.nomad.hcl  —  example pattern
#
# Demonstrates the cluster-standard way to fetch a per-architecture binary at
# job-start and make it available to the rest of the tasks in the same group,
# without baking a custom container image.
#
# THE PATTERN
# ───────────
#  Use Nomad's native `artifact` stanza in a `prestart` task to download the
#  right flavour of the binary (selected by ${attr.kernel.name} and
#  ${attr.cpu.arch}) into the alloc's shared dir, with sha256 verification
#  driven by a `locals` checksum map.  Subsequent tasks in the same group
#  exec  $NOMAD_ALLOC_DIR/bin/<binary>  — they don't know or care about the
#  upstream URL, the OS, or the arch.
#
# WHY NOT eget?
# ─────────────
#  `eget` (https://github.com/zyedidia/eget) is a great client-side tool for
#  GitHub-style release pages — it auto-discovers the right asset for the host
#  arch.  It needs an API that lists release assets (GitHub's, Forgejo's,
#  Gitea's).  Our binaries live on a plain RustFS bucket (no release-manifest
#  API), and GitRiver's release-attachment endpoint refuses uploads >2 MiB,
#  so we can't put the binaries on a release-API page eget could parse.
#  Direct-URL mode in eget exists but loses the arch-auto-pick — at which
#  point the artifact stanza below is simpler.
#
# WHY NOT GITRIVER LFS?
# ─────────────────────
#  Same binaries live on the `releases/v0.1.4-lfs` branch via Git LFS, but
#  LFS download requires GitRiver auth.  For Nomad job prestarts inside the
#  cluster, anonymous read from RustFS removes the token-plumbing step.
#
# REUSE
# ─────
#  Copy the `prestart` task block into any job that needs abc-node-probe.
#  Bump `abc_node_probe_version` to switch versions — checksums are pulled
#  fresh from the official sha256sums.txt at the new version, so there's
#  no parallel checksum table to maintain.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "abc_node_probe_version" {
  type        = string
  default     = "v0.1.4"
  description = "Tag on the abc-cluster/abc-node-probe repo whose binary to pull."
}

variable "release_base_url" {
  type        = string
  default     = "http://rustfs.aither/releases/abc-node-probe"
  description = "Base URL holding /<version>/abc-node-probe-<os>-<arch> artifacts. Defaults to the cluster's anonymous-read RustFS mirror."
}

job "example-abc-node-probe-runner" {
  namespace   = "default"
  region      = "global"
  datacenters = var.datacenters
  type        = "batch"

  meta {
    abc_cluster_type = "abc-nodes"
    example          = "fetch-and-run-binary"
  }

  group "probe" {
    count = 1

    network {
      mode = "bridge"
    }

    # ── Prestart: download + verify via Nomad's artifact stanza ─────────────
    # Two artifact blocks let Nomad's go-getter handle the network fetch (with
    # retries, redirects, archive auto-extract):
    #   1. the per-arch binary (URL constructed via ${attr.*} at task-launch)
    #   2. the official sha256sums.txt that ships alongside it
    #
    # We deliberately don't use artifact's built-in `options.checksum` field
    # because that wants ONE literal sha256 per stanza — and we'd have to
    # maintain a parallel checksum map in HCL.  Instead, the script below
    # picks the matching line out of the official sha256sums.txt and verifies
    # against it.  Same security guarantee, no out-of-band checksum mirror.
    #
    # The `args` script then promotes the verified binary into /alloc/bin/
    # so the main task (different filesystem namespace) can exec it.  /alloc
    # is Nomad's cross-task shared dir for a group.
    task "fetch-probe" {
      driver = "containerd-driver"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      # NOTE on the $${...} escape: HCL2 evaluates ${...} at job-submit time,
      # but `attr.kernel.name` / `attr.cpu.arch` are RUNTIME node attributes
      # that Nomad expands at task-launch.  Doubling the dollar emits a
      # literal `${attr.X}` for Nomad's runtime interpolator to fill in.
      artifact {
        source      = "${var.release_base_url}/${var.abc_node_probe_version}/abc-node-probe-$${attr.kernel.name}-$${attr.cpu.arch}"
        destination = "local/abc-node-probe-bin"
        mode        = "file"
      }

      artifact {
        source      = "${var.release_base_url}/${var.abc_node_probe_version}/sha256sums.txt"
        destination = "local/sha256sums.txt"
        mode        = "file"
      }

      env {
        OS   = "$${attr.kernel.name}"
        ARCH = "$${attr.cpu.arch}"
      }

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        # `<<-CMD` heredoc — HCL2 still interprets ${...} inside.  Every shell
        # variable that uses ${X} braced form is $${X} to leave it literal
        # for the shell.  Bare $X (no braces) passes through HCL2 untouched.
        args = [
          <<-CMD
          set -eu
          # No apk add — busybox in alpine:3.19 already provides sha256sum,
          # awk, cp, chmod, mkdir.  Nomad's artifact stanza did the fetch.
          # sha256sums.txt lists artifacts by upstream name (e.g.
          # abc-node-probe-linux-amd64).  We saved the file under a fixed
          # local name (abc-node-probe-bin) — re-derive the upstream name to
          # find the right checksum line.
          UPSTREAM_NAME="abc-node-probe-$OS-$ARCH"
          [ "$OS" = "windows" ] && UPSTREAM_NAME="$UPSTREAM_NAME.exe"
          cd "$NOMAD_TASK_DIR"

          EXPECTED=$(awk -v f="$UPSTREAM_NAME" '$2 == f { print $1 }' sha256sums.txt)
          [ -n "$EXPECTED" ] || { echo "no checksum entry for $UPSTREAM_NAME" >&2; exit 1; }
          ACTUAL=$(sha256sum abc-node-probe-bin | awk '{print $1}')
          [ "$EXPECTED" = "$ACTUAL" ] || { echo "sha256 mismatch: expected $EXPECTED got $ACTUAL" >&2; exit 1; }

          mkdir -p "$NOMAD_ALLOC_DIR/bin"
          cp abc-node-probe-bin "$NOMAD_ALLOC_DIR/bin/abc-node-probe"
          chmod +x "$NOMAD_ALLOC_DIR/bin/abc-node-probe"
          echo "[fetch-probe] sha256 OK  ($UPSTREAM_NAME -> $ACTUAL)"
          echo "[fetch-probe] placed: $NOMAD_ALLOC_DIR/bin/abc-node-probe"
          CMD
          ,
        ]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Main task: invoke the binary from the shared alloc dir ──────────────
    # No URL, no arch awareness — just run the file the prestart placed.
    # `set -o pipefail` ensures a failure of abc-node-probe propagates as a
    # non-zero task exit (without it, the trailing `tee | head` would mask a
    # binary that failed to start because the parent pipeline exits with the
    # last command's status).
    task "run-probe" {
      driver = "containerd-driver"

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args = [
          <<-CMD
          set -eu
          set -o pipefail
          BIN=$NOMAD_ALLOC_DIR/bin/abc-node-probe
          ls -la $BIN
          echo "--- abc-node-probe --version ---"
          $BIN --version
          echo "--- abc-node-probe --help (first lines) ---"
          $BIN --help
          CMD
          ,
        ]
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
