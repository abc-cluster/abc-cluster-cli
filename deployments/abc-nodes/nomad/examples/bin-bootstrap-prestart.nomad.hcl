# bin-bootstrap-prestart.nomad.hcl  —  cluster-standard binary bootstrap pattern
#
# Demonstrates how to stage one or more tool binaries for the current node
# architecture before a batch job's main task runs, without baking a container
# image and without assuming the tool is pre-installed on the node.
#
# ── DESIGN ──────────────────────────────────────────────────────────────────
#
#  A `prestart` lifecycle task (driver = raw_exec) runs before the main task.
#  It downloads the required binary into the allocation's shared directory
#  ($NOMAD_ALLOC_DIR/bin/) which is visible to all tasks in the same group.
#  The main task just prepends that dir to $PATH and runs normally.
#
#  Binary source priority:
#    1. Internal RustFS public bucket  — fast, no rate limits, arch-specific
#       pre-built binaries, accessible to all Tailscale-connected nodes.
#    2. Official GitHub release         — fallback for cloud / internet nodes
#       that can't reach the Tailscale network.  Uses `eget` for clean
#       asset selection (auto-detects OS + arch, handles .tar.gz / .zip).
#
#  eget itself is bootstrapped the same way (RustFS → GitHub), so the
#  whole chain works cold on any Linux node that has curl or wget.
#
# ── WHY eget FOR GITHUB RELEASES? ───────────────────────────────────────────
#
#  GitHub releases use inconsistent naming across projects:
#    s5cmd: s5cmd_2.1.0_Linux-64bit.tar.gz  (64bit, not amd64)
#    rclone: rclone-v1.77.0-linux-amd64.zip
#    eget:   eget-1.3.3-linux_amd64.tar.gz
#
#  Mapping from `uname -m` to each project's naming convention is fragile
#  boilerplate.  eget understands this: `eget peak/s5cmd` picks the right
#  asset for the current host without any naming-convention knowledge.
#
#  eget direct-URL mode (`eget https://...`) also works but loses the
#  arch-auto-pick — not useful for our RustFS binaries which already have
#  explicit arch in the filename.  We use plain curl for RustFS.
#
# ── WHY NOT Nomad's artifact stanza for the GitHub fallback? ─────────────────
#
#  Nomad's `artifact` stanza is ideal when:
#    • binaries use normalised arch names (linux-amd64, linux-arm64), AND
#    • all cluster nodes can reach the source URL.
#  For RustFS: artifact stanza works perfectly (see abc-node-probe-runner.nomad.hcl).
#  For GitHub: project-specific arch suffixes (64bit, armv7, etc.) make a single
#  artifact URL awkward, and the stanza can't fall back on failure.
#  Use the prestart script approach shown here when the cluster spans Tailscale
#  + cloud nodes that may not reach the internal RustFS.
#
# ── HCL ESCAPING NOTE ────────────────────────────────────────────────────────
#
#  Inside HCL heredoc strings, ${...} triggers HCL interpolation.
#  Bash variable references that use braces must be written $${VAR} so HCL
#  emits the literal ${VAR} that bash then expands at runtime.
#  Bare $VAR (no braces) passes through HCL unchanged — use it wherever the
#  variable name is unambiguous (not followed by a word character).
#  Nomad runtime variables like $NOMAD_ALLOC_DIR are bash-level and safe.
#  Consul Template directives {{ env "KEY" }} are evaluated at template-render
#  time (before task start) and bake in values from the task env block.
#
# ── USAGE ────────────────────────────────────────────────────────────────────
#
#  abc job run \
#    --var source='s3://my-bucket/path/*.gz' \
#    --var destination=/scratch/data \
#    bin-bootstrap-prestart.nomad.hcl
#
#  (or: abc job run bin-bootstrap-prestart.nomad.hcl   to use defaults)
#
# ── ADDING MORE TOOLS ────────────────────────────────────────────────────────
#
#  In the setup-bins task template, add another install_tool call:
#
#    install_tool \
#      "rclone" \
#      "$RUSTFS_BASE/releases/rclone/v1.77.0/rclone-linux-$ARCH" \
#      "rclone/rclone" "v1.77.0"
#

# ── Variables ────────────────────────────────────────────────────────────────

variable "namespace" {
  type    = string
  default = "default"
}

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "rustfs_base" {
  type        = string
  default     = "http://rustfs.aither"
  description = "Base URL of the cluster's public RustFS bucket (Tailscale-accessible). Primary binary source."
}

variable "s5cmd_version" {
  type        = string
  default     = "v2.1.0"
  description = "s5cmd release tag to stage."
}

variable "eget_version" {
  type        = string
  default     = "v1.3.3"
  description = "eget release tag used to download GitHub release binaries."
}

variable "source" {
  type        = string
  default     = "s3://my-bucket/path/*.gz"
  description = "Source URL passed to s5cmd cp."
}

variable "destination" {
  type        = string
  default     = "/tmp/abc-data-download"
  description = "Destination path or s3:// URI for downloaded files."
}

variable "tool_args" {
  type        = string
  default     = ""
  description = "Extra global flags for s5cmd (e.g. '--no-sign-request'). Placed before the subcommand."
}

variable "num_workers" {
  type        = string
  default     = "4"
  description = "s5cmd --numworkers value."
}

variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS_DEFAULT_REGION injected for s5cmd; overrides node defaults."
}

# ── Job ──────────────────────────────────────────────────────────────────────

job "abc-data-download-s5cmd" {
  type        = "batch"
  namespace   = var.namespace
  region      = "global"
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    pattern          = "bin-bootstrap-prestart"
  }

  group "main" {
    count = 1

    # Fail fast — no retries or rescheduling on batch download jobs.
    restart {
      attempts = 0
    }

    reschedule {
      attempts  = 0
      unlimited = false
    }

    # ── Prestart: stage tool binaries ────────────────────────────────────────
    #
    # Runs to completion before "main" starts.  Places binaries in
    # $NOMAD_ALLOC_DIR/bin/ — Nomad's cross-task shared directory.
    task "setup-bins" {
      driver = "raw_exec"

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      config {
        command = "/bin/bash"
        args    = ["$${NOMAD_TASK_DIR}/setup.sh"]
      }

      # Template rendered by Consul Template before the task starts.
      # {{ env "KEY" }} reads from the task env block below (baked in at render).
      # $${VAR} → HCL emits ${VAR} → bash expands at runtime.
      # $VAR (no braces) passes through HCL unchanged.
      template {
        data = <<-EOT
          #!/bin/bash
          set -euo pipefail

          # Values baked in by Consul Template from the task env block.
          RUSTFS_BASE="{{ env "RUSTFS_BASE" }}"
          EGET_VERSION="{{ env "EGET_VERSION" }}"
          EGET_FILE_VERSION="{{ env "EGET_FILE_VERSION" }}"
          S5CMD_VERSION="{{ env "S5CMD_VERSION" }}"

          # Shared bin dir — all tasks in this group see the same NOMAD_ALLOC_DIR.
          BIN_DIR="$NOMAD_ALLOC_DIR/bin"
          mkdir -p "$BIN_DIR"

          # ── Arch detection ─────────────────────────────────────────────────
          # Normalise uname -m to amd64 / arm64.
          # Our RustFS binaries and eget GitHub assets use these conventions.
          RAW_ARCH=$(uname -m)
          case "$RAW_ARCH" in
            aarch64|arm64) ARCH=arm64 ;;
            x86_64|amd64)  ARCH=amd64 ;;
            *) echo "[setup] unsupported arch: $RAW_ARCH" >&2; exit 1 ;;
          esac

          echo "[setup] node arch=$ARCH  bin_dir=$BIN_DIR"

          # ── HTTP fetch helpers ──────────────────────────────────────────────
          # fetch_direct: plain download (follows redirects, retries not included).
          fetch_direct() {
            local url="$1" dest="$2"
            if command -v curl >/dev/null 2>&1; then
              curl -fsSL -o "$dest" "$url"
            elif command -v wget >/dev/null 2>&1; then
              wget -qO "$dest" "$url"
            else
              echo "[setup] ERROR: neither curl nor wget available" >&2; return 1
            fi
          }

          # fetch_probe: same, but with a short connect-timeout so internal URLs
          # that are unreachable from cloud nodes fail fast instead of hanging.
          fetch_probe() {
            local url="$1" dest="$2"
            if command -v curl >/dev/null 2>&1; then
              curl -fsSL --connect-timeout 5 -o "$dest" "$url" 2>/dev/null
            elif command -v wget >/dev/null 2>&1; then
              wget -T 5 -qO "$dest" "$url" 2>/dev/null
            else
              return 1
            fi
          }

          # ── Bootstrap eget ──────────────────────────────────────────────────
          # eget handles GitHub releases cleanly: picks the right OS/arch asset,
          # extracts archives, sets the executable bit.
          # Ref: https://github.com/zyedidia/eget
          #
          # Bootstrap strategy: try RustFS plain binary first (fast, Tailscale nodes);
          # fall back to the official GitHub tarball.
          # Tarball path: eget-<version>-linux_<arch>/eget  (e.g. eget-1.3.3-linux_amd64/eget)
          EGET="$BIN_DIR/eget"

          if ! [ -x "$EGET" ]; then
            echo "[setup] bootstrapping eget $EGET_VERSION..."
            TMP_TAR="$NOMAD_TASK_DIR/eget.tar.gz"

            EGET_RUSTFS_URL="$RUSTFS_BASE/releases/eget/$EGET_VERSION/eget-linux-$ARCH"
            EGET_GH_URL="https://github.com/zyedidia/eget/releases/download/$EGET_VERSION/eget-$EGET_FILE_VERSION-linux_$ARCH.tar.gz"

            if fetch_probe "$EGET_RUSTFS_URL" "$EGET"; then
              echo "[setup] eget ← RustFS (plain binary)"
            else
              echo "[setup] RustFS miss; downloading eget from GitHub..."
              fetch_direct "$EGET_GH_URL" "$TMP_TAR"
              # Extract just the binary from eget-1.3.3-linux_amd64/eget.
              tar -xzf "$TMP_TAR" --strip-components=1 -C "$BIN_DIR" \
                "eget-$EGET_FILE_VERSION-linux_$ARCH/eget"
              rm -f "$TMP_TAR"
              echo "[setup] eget ← GitHub"
            fi
            chmod +x "$EGET"
          fi

          # ── install_tool ────────────────────────────────────────────────────
          # Download a binary into BIN_DIR.
          #
          # Usage:
          #   install_tool <name> <rustfs_url> <github_repo> <github_tag>
          #
          # Tries RustFS first (fast, no rate limits, connect-timeout = 5 s).
          # Falls back to eget which auto-selects the right asset by OS/arch.
          install_tool() {
            local name=$1 rustfs_url=$2 github_repo=$3 github_tag=$4
            local dest="$BIN_DIR/$name"

            echo "[setup] installing $name..."
            if fetch_probe "$rustfs_url" "$dest"; then
              echo "[setup] $name ← RustFS"
            else
              echo "[setup] $name: RustFS miss; downloading from GitHub ($github_repo@$github_tag)..."
              "$EGET" "$github_repo" --tag "$github_tag" --to "$dest"
              echo "[setup] $name ← GitHub (eget)"
            fi
            chmod +x "$dest"
          }

          # ── Stage required binaries ─────────────────────────────────────────
          # Add install_tool calls here for each binary the job needs.

          install_tool \
            "s5cmd" \
            "$RUSTFS_BASE/releases/s5cmd/$S5CMD_VERSION/s5cmd_linux_$ARCH" \
            "peak/s5cmd" \
            "$S5CMD_VERSION"

          # Uncomment to also stage rclone:
          # install_tool \
          #   "rclone" \
          #   "$RUSTFS_BASE/releases/rclone/v1.77.0/rclone-linux-$ARCH" \
          #   "rclone/rclone" \
          #   "v1.77.0"

          echo "[setup] ready — binaries staged:"
          ls -lh "$BIN_DIR/"
        EOT

        destination = "local/setup.sh"
        perms       = "0755"
      }

      env {
        # Expose job variables to Consul Template ({{ env "KEY" }}).
        # HCL functions are evaluated at job-submission time.
        RUSTFS_BASE       = var.rustfs_base
        EGET_VERSION      = var.eget_version
        # Strip the leading "v" for filenames (v1.3.3 → 1.3.3).
        # HCL's trimprefix() avoids needing ${EGET_VERSION#v} in bash (which
        # would require $${EGET_VERSION#v} escaping inside HCL heredocs).
        EGET_FILE_VERSION = trimprefix(var.eget_version, "v")
        S5CMD_VERSION     = var.s5cmd_version
      }

      resources {
        cpu    = 300
        memory = 128
      }
    }

    # ── Main task ─────────────────────────────────────────────────────────────
    #
    # Uses the binaries staged by setup-bins.  Both tasks share NOMAD_ALLOC_DIR
    # so $NOMAD_ALLOC_DIR/bin/ is visible here without any extra setup.
    task "main" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["$${NOMAD_TASK_DIR}/run.sh"]
      }

      template {
        data = <<-EOT
          #!/bin/bash
          set -euo pipefail

          # Binaries staged by the prestart task.
          export PATH="$NOMAD_ALLOC_DIR/bin:$PATH"

          echo "[main] s5cmd version: $(s5cmd version)"

          # Job parameters baked in by Consul Template.
          SOURCE="{{ env "DOWNLOAD_SOURCE" }}"
          DEST="{{ env "DOWNLOAD_DEST" }}"
          TOOL_ARGS="{{ env "TOOL_ARGS" }}"
          NUM_WORKERS="{{ env "NUM_WORKERS" }}"
          export AWS_DEFAULT_REGION="{{ env "AWS_REGION" }}"

          # Create local destination if it is not an s3:// URI.
          if [[ "$DEST" != s3://* ]]; then
            mkdir -p "$DEST"
          fi

          echo "[main] $SOURCE → $DEST"

          # TOOL_ARGS is intentionally word-split (it's a list of flags).
          # shellcheck disable=SC2086
          s5cmd $TOOL_ARGS --numworkers "$NUM_WORKERS" cp "$SOURCE" "$DEST"

          echo "[main] done."
        EOT

        destination = "local/run.sh"
        perms       = "0755"
      }

      env {
        DOWNLOAD_SOURCE = var.source
        DOWNLOAD_DEST   = var.destination
        TOOL_ARGS       = var.tool_args
        NUM_WORKERS     = var.num_workers
        AWS_REGION      = var.aws_region
      }

      resources {
        cpu    = 2000
        memory = 512
      }
    }
  }
}
