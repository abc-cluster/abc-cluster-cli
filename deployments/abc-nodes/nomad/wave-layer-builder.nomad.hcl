# wave-layer-builder.nomad.hcl  —  REFERENCE TEMPLATE
#
# This file documents what `abc admin tools wave-layer` submits to Nomad.
# The CLI generates the actual HCL programmatically (cmd/admin/tools/wavelayer.go)
# with arch, bucket, endpoint, and tool list baked in as literals at submit time.
#
# You do NOT register this file manually.  Use:
#   abc admin tools wave-layer [arch...]
#
# WHAT THE SUBMITTED JOB DOES
# ─────────────────────────────
#  1. Nomad fetches s5cmd from abc-reserved/binary_tools/s5cmd-linux-<arch>
#     via the artifact stanza (public-read, no credentials needed for download).
#  2. Consul Template reads S3 write credentials from:
#       nomad/jobs/abc-nodes-storage  (access_key_id, secret_access_key, endpoint)
#  3. The build script:
#       a. Downloads tools.toml from abc-reserved/binary_tools/tools.toml
#       b. Parses tools with wave_inject = true
#       c. Downloads each <tool>-linux-<arch> binary
#       d. For each tool, packs usr/local/bin/<tool> into a per-tool tar.gz
#          → wave-layer-linux-<arch>-<tool>.tar.gz
#       e. Packs all tools together into a combined tar.gz
#          → wave-layer-linux-<arch>.tar.gz
#       f. Uploads both sets to abc-reserved/wave_lite_layers/
#
# LAYER NAMING CONVENTION
# ─────────────────────────────
#  Combined (all wave_inject tools):
#    wave-layer-linux-amd64.tar.gz
#    wave-layer-linux-arm64.tar.gz
#  Per-tool (one binary each):
#    wave-layer-linux-amd64-s5cmd.tar.gz
#    wave-layer-linux-amd64-rclone.tar.gz
#    wave-layer-linux-arm64-s5cmd.tar.gz
#    … etc.
#
#  abc job run selects layers at submit time:
#    --wave-inject-tools          → combined layer (all tools)
#    --wave-inject-tools=s5cmd    → per-tool layer (s5cmd only)
#    --wave-inject-tools=s5cmd,rclone → two per-tool layers merged by Wave
#
# CREDENTIAL SETUP (one-time)
# ─────────────────────────────
#   abc admin services nomad cli -- var put nomad/jobs/abc-nodes-storage \
#     access_key_id=rustfsadmin \
#     secret_access_key=<secret> \
#     endpoint=http://rustfs.aither
#
# USAGE
# ─────
#   abc admin tools wave-layer                   # amd64 + arm64
#   abc admin tools wave-layer amd64 --wait      # one arch, block until done
#   abc admin tools wave-layer --tools=samtools  # selective rebuild
#   abc admin tools wave-layer --dry-run         # preview generated HCL

# ── Example of a generated one-off job (arch=amd64, illustrative) ────────────

job "wave-layer-amd64" {
  namespace   = "abc-services"
  type        = "batch"
  region      = "global"
  datacenters = ["dc1", "default"]
  priority    = 40

  meta {
    abc_cluster_type = "abc-nodes"
    managed_by       = "abc-cluster-cli"
    wave_layer_arch  = "amd64"
    wave_layer_tools = "*"
  }

  group "build" {
    count = 1

    restart {
      attempts = 1
      delay    = "10s"
      mode     = "fail"
    }

    reschedule {
      attempts  = 0
      unlimited = false
    }

    task "build-layer" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["${NOMAD_TASK_DIR}/build.sh"]
      }

      # s5cmd fetched directly — no prestart task, no credentials needed (public-read).
      artifact {
        source      = "http://rustfs.aither/abc-reserved/binary_tools/s5cmd-linux-amd64"
        destination = "local/s5cmd"
        mode        = "file"
      }

      # S3 write credentials from the shared Nomad Variable.
      template {
        data = <<-CREDS
{{- with nomadVar "nomad/jobs/abc-nodes-storage" -}}
AWS_ACCESS_KEY_ID={{ .access_key_id }}
AWS_SECRET_ACCESS_KEY={{ .secret_access_key }}
S3_ENDPOINT={{ .endpoint }}
{{- end }}
CREDS
        destination = "secrets/s3.env"
        env         = false
      }

      template {
        data        = <<-'SCRIPT'
#!/bin/bash
set -euo pipefail
ARCH="amd64"
BUCKET="abc-reserved"
SRC_PREFIX="binary_tools"
DEST_PREFIX="wave_lite_layers"
TOOLS_FILTER="*"
TOML_URL="http://rustfs.aither/abc-reserved/binary_tools/tools.toml"

source "$NOMAD_SECRETS_DIR/s3.env"
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION="us-east-1"

S5="$NOMAD_TASK_DIR/s5cmd"
chmod +x "$S5"

WORK="$NOMAD_TASK_DIR/work"
STAGE="$WORK/stage/usr/local/bin"
mkdir -p "$STAGE"

curl -fsSL -o "$WORK/tools.toml" "$TOML_URL"

# (parse tools.toml, download binaries, pack tar.gz, upload — see wavelayer.go)

REMOTE="s3://$BUCKET/$DEST_PREFIX/wave-layer-linux-$ARCH.tar.gz"
"$S5" --endpoint-url "$S3_ENDPOINT" cp "$WORK/wave-layer-linux-$ARCH.tar.gz" "$REMOTE"
echo "[wave-layer] done"
SCRIPT
        destination = "local/build.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
}
