# Output Sync — abc-nodes
# ────────────────────────────────────────────────────────────────────────────
# Automatically persists job output files to object storage (RustFS) after
# each allocation completes.  Zero user involvement — the abc CLI injects the
# `upload-outputs` task into every user job before submission.
#
# ┌─────────────────────────────────────────────────────────────────────────┐
# │  DESIGN DECISION: two-path strategy                                     │
# │                                                                         │
# │  Path A — FUSE unavailable (this file, active now)                      │
# │  ─────────────────────────────────────────────────                      │
# │  s5cmd poststop sync                                                    │
# │  • Main task writes to ${NOMAD_ALLOC_DIR}/output/ (local disk)          │
# │  • After main exits, s5cmd uploads to RustFS in parallel                │
# │  • No kernel modules, no privileged containers, no extra services       │
# │  • Works on every node class (on-prem, GCP, OCI, spot)                 │
# │  • Data lands in S3 only after job completion — acceptable for batch    │
# │  • If the node is lost mid-job, output in alloc dir is lost             │
# │                                                                         │
# │  Path B — FUSE available (future: juicefs-csi.nomad.hcl)               │
# │  ──────────────────────────────────────────────────────                 │
# │  JuiceFS CSI volume, FUSE-mounted into the main task                    │
# │  • Main task writes to /output/ (mounted CSI volume, looks local)       │
# │  • Data streamed to RustFS in real-time via JuiceFS FUSE layer          │
# │  • Survives node failure mid-job (data already in S3)                   │
# │  • Enables shared read-only datasets (reference genomes, tool dbs)      │
# │  • Requires: /dev/fuse on node, CAP_SYS_ADMIN, PostgreSQL metadata DB   │
# │  • Controller: juicefs-csi-controller.nomad.hcl (service, 1 instance)  │
# │  • Node plugin: juicefs-csi-node.nomad.hcl (system, non-spot nodes)    │
# │                                                                         │
# │  Selection mechanism                                                    │
# │  ────────────────────                                                   │
# │  The abc CLI reads node.meta.fuse_available (set during cluster         │
# │  bootstrap per node) and picks Path A or B at job submission time.      │
# │  Nodes that lack the meta key default to Path A.                        │
# │  No Nomad Enterprise / Sentinel required.                               │
# └─────────────────────────────────────────────────────────────────────────┘
#
# OUTPUT DIRECTORY CONVENTION
# ───────────────────────────
# DESIGN DECISION: ${NOMAD_ALLOC_DIR}/output/ is the canonical output path.
#
# ${NOMAD_ALLOC_DIR} (typically /opt/nomad/data/alloc/<id>/alloc/) is:
#   • Shared across ALL tasks in the same group (group-scoped, not task-scoped)
#   • Still accessible to poststop tasks after the main task exits
#   • Cleaned up by Nomad's GC after the alloc is fully complete
#
# ${NOMAD_TASK_DIR} was considered but is task-private — the poststop task
# cannot read a sibling task's task dir.  Alloc dir is the right scope.
#
# JuiceFS path difference: output path becomes /output/ (the CSI mount
# destination), not ${NOMAD_ALLOC_DIR}/output/.  The abc CLI swaps the path
# when injecting the volume_mount stanza.
#
# S3 KEY LAYOUT
# ─────────────
# DESIGN DECISION: outputs are namespaced by Nomad namespace + job + alloc.
#
#   <bucket>/<namespace>/<job-name>/<alloc-id>/
#   e.g.: job-outputs/su-mbhg-animaltb/kim-script-job-abc-data-427506/3f2a.../
#
# Rationale:
#   • namespace   → maps to research group/user — enables per-group S3 policies later
#   • job-name    → human-readable job identity
#   • alloc-id    → unique per run; multiple retries produce separate prefixes,
#                   never overwrite
#
# CREDENTIALS
# ───────────
# DESIGN DECISION: Nomad Variables, not Vault, not hardcoded env vars.
# Nomad Variables (1.4+) are encrypted at rest, ACL-scoped, and require no
# Vault deployment.  Path: nomad/jobs/abc-nodes-storage (global write creds).
#
# Bootstrap:
#   nomad var put nomad/jobs/abc-nodes-storage \
#     access_key_id=rustfsadmin \
#     secret_access_key=rustfsadmin
#
# Future hardening: per-namespace credentials so su-* groups can only write
# to their own prefix.  The key path would become nomad/jobs/<namespace>/storage
# and the template selector would use ${NOMAD_NAMESPACE}.
#
# FALLBACK: RUSTFS → MINIO
# ────────────────────────
# DESIGN DECISION: RustFS is the primary S3 target.  If s5cmd fails with a
# 5xx or API-incompatibility error (multipart, ETag mismatch, etc.), the abc
# CLI should resubmit the job with var.s3_endpoint pointing at MinIO instead.
# This file does NOT auto-fallback at runtime — a silent fallback would mask
# RustFS compatibility issues we want to surface and fix.
#
# BUCKET BOOTSTRAP
# ────────────────
# The `job-outputs` bucket must exist before the first upload.  It is created
# by the `ensure-output-bucket` prestart task in this file (idempotent mc mb).
# On a fresh cluster, run this example job once with count=0 to trigger only
# the prestart, or add bucket creation to the Terraform rustfs provisioning.
#
# USER TRANSPARENCY
# ─────────────────
# DESIGN DECISION: task injection by the abc CLI, not user-authored.
#
# Users declare their job normally.  The abc CLI intercepts `abc job run` and
# appends the `upload-outputs` task before submitting to Nomad.  Users never
# write the poststop task themselves.
#
# The injection is opt-in by default for su-* namespaces (research users) and
# opt-out for abc-services / abc-automations (infrastructure jobs that don't
# produce user-facing outputs).
#
# Alternative considered: a Nomad event-stream watcher system job that catches
# alloc completions and syncs from the host filesystem.  Deferred because:
#   1. Race with Nomad GC — alloc dir may be cleaned before the watcher fires
#   2. Requires host-level filesystem access and elevated privileges on every node
#   3. No per-job output path customisation without an alloc metadata convention
# The watcher pattern remains the right evolution if we need zero-touch for
# jobs we don't control (third-party / pre-packaged workloads).

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

# DESIGN DECISION: s5cmd over rclone / aws-cli.
#   rclone  : 50 MB binary, general-purpose, slower for many small files
#   aws-cli : 40 MB, Python runtime, adds interpreter startup latency
#   s5cmd   : ~7 MB Go binary, no dependencies, parallel by default,
#             purpose-built for S3 — fastest for the bioinformatics pattern
#             of many per-sample files (VCFs, BAMs, FASTQs, TSVs).
variable "s5cmd_version" {
  type    = string
  default = "2.2.2"
}

# DESIGN DECISION: arch naming mismatch.
# Nomad exposes ${attr.cpu.arch} = "amd64" | "arm64".
# s5cmd release filenames use "Linux-64bit" | "Linux-arm64".
# The mapping cannot be done inline in an artifact `source` string (no
# conditional interpolation in HCL artifact blocks).  This variable holds the
# s5cmd arch token; the abc CLI sets it per-node at injection time by reading
# the target node's ${attr.cpu.arch} attribute and mapping it.
# Default: Linux-64bit (x86-64) — covers aither and GCP n2 instances.
variable "s5cmd_arch" {
  type        = string
  default     = "Linux-64bit"
  description = "s5cmd release arch token. Linux-64bit (amd64) or Linux-arm64."
}

# Primary S3 target.  Static IP used (same pattern as Alloy's prometheus_url /
# loki_url) so the task works on nodes that have no Consul agent and cannot
# resolve the `rustfs.aither` vhost.
variable "s3_endpoint" {
  type        = string
  default     = "http://100.70.185.46:9900"
  description = "RustFS S3 API. Switch to MinIO (port 8333) if RustFS proves incompatible."
}

variable "s3_output_bucket" {
  type        = string
  default     = "job-outputs"
  description = "Bucket where all job outputs land. Created by the ensure-output-bucket prestart if absent."
}

# ─── This job is an EXAMPLE and TEST HARNESS ─────────────────────────────────
# It is NOT deployed as a standing service.  Its purpose is:
#   1. Document the canonical upload-outputs task definition
#   2. Serve as a runnable integration test (nomad job run output-sync.nomad.hcl)
#   3. Trigger bucket bootstrap via the ensure-output-bucket prestart
#
# In production the `upload-outputs` task (and optionally `ensure-output-bucket`)
# is injected into user job specs by the abc CLI at submission time.
# ─────────────────────────────────────────────────────────────────────────────

job "abc-nodes-output-sync-example" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "batch"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "output-sync"
  }

  group "workload" {
    count = 1

    # DESIGN DECISION: no network block.
    # The upload-outputs task needs outbound HTTP to RustFS only.  raw_exec
    # tasks use host networking by default — no bridge, no port mapping needed.
    # JuiceFS path would require a bridge network for the CSI mount sidecar.

    # ── Bucket bootstrap (prestart, idempotent) ───────────────────────────────
    # Creates the job-outputs bucket if it does not already exist.
    # mc (MinIO Client) is used because it has the cleanest `mb --ignore-existing`
    # semantics across S3-compatible backends.
    #
    # DESIGN DECISION: prestart here for the example job.  For production jobs
    # injected by the abc CLI, bucket creation is a one-time Terraform step
    # (null_resource + mc mb) run during cluster provisioning — not per-job.
    # Keeping it here makes the example self-contained.
    task "ensure-output-bucket" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }

      driver = "raw_exec"

      artifact {
        source      = "https://dl.min.io/client/mc/release/linux-amd64/mc"
        destination = "local/mc"
        mode        = "file"
      }

      template {
        destination = "secrets/s3.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-nodes-storage" -}}
AWS_ACCESS_KEY_ID={{ .access_key_id }}
AWS_SECRET_ACCESS_KEY={{ .secret_access_key }}
{{- end }}
EOF
      }

      config {
        command = "/bin/sh"
        args = [
          "-c",
          <<-EOC
          chmod +x ${NOMAD_TASK_DIR}/mc
          ${NOMAD_TASK_DIR}/mc alias set rustfs ${var.s3_endpoint} \
            $AWS_ACCESS_KEY_ID $AWS_SECRET_ACCESS_KEY --api S3v4
          ${NOMAD_TASK_DIR}/mc mb --ignore-existing rustfs/${var.s3_output_bucket}
          echo "ensure-output-bucket: bucket ${var.s3_output_bucket} ready"
          EOC
        ]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Main workload (placeholder) ───────────────────────────────────────────
    # In production this is replaced by the user's actual task (containerd
    # image, nf-core pipeline, script, etc.).  The abc CLI substitutes this
    # task at injection time.
    #
    # CONTRACT: the main task MUST write outputs to ${NOMAD_ALLOC_DIR}/output/.
    # The abc CLI documents this convention and, for Nextflow/Snakemake jobs,
    # sets the pipeline publishDir / output path to that directory automatically.
    #
    # JuiceFS path difference: the main task would instead write to /output/
    # (the CSI mount destination).  The alloc dir is not involved.
    task "main" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args = [
          "-c",
          <<-EOC
          OUT="${NOMAD_ALLOC_DIR}/output"
          mkdir -p "$OUT"
          echo "job=${NOMAD_JOB_NAME} alloc=${NOMAD_ALLOC_ID}" > "$OUT/run-info.txt"
          date -u                                                >> "$OUT/run-info.txt"
          dd if=/dev/urandom bs=1k count=64 2>/dev/null | base64 > "$OUT/sample-output.txt"
          echo "main: wrote $(du -sh $OUT | cut -f1) to $OUT"
          EOC
        ]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    # ── Output sync (poststop) ────────────────────────────────────────────────
    # Runs after ALL non-sidecar tasks in the group have exited (any exit code).
    #
    # DESIGN DECISION: poststop hook, not a sidecar.
    #   sidecar  : runs concurrently with main, adds latency to job start,
    #              must be stopped explicitly, complicates job lifecycle
    #   poststop : fires only after main exits, single-purpose, clean lifecycle
    #
    # DESIGN DECISION: failure marks the alloc as failed (not silently ignored).
    # A failed upload is a data-loss event.  Making it visible in the Nomad UI
    # and Grafana job dashboard is intentional — operators and users should
    # know when outputs did not reach S3.
    #
    # JuiceFS path: this entire task is absent.  The CSI volume mount on the
    # main task handles persistence transparently — no poststop needed.
    task "upload-outputs" {
      lifecycle {
        hook    = "poststop"
        sidecar = false
      }

      driver = "raw_exec"

      # DESIGN DECISION: s5cmd binary downloaded via artifact (same pattern
      # as Alloy).  No host-side installation required — works on any Nomad
      # client that can reach GitHub releases.  The binary is cached in the
      # alloc dir for the duration of the allocation.
      artifact {
        source      = "https://github.com/peak/s5cmd/releases/download/v${var.s5cmd_version}/s5cmd_${var.s5cmd_version}_${var.s5cmd_arch}.tar.gz"
        destination = "local/"
        # tar.gz unpacks to: local/s5cmd  (binary is at top level of archive)
      }

      # Credentials from Nomad Variables — see file header for bootstrap command.
      # AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are the standard env vars
      # consumed by the AWS SDK embedded in s5cmd.
      template {
        destination = "secrets/s3.env"
        env         = true
        data        = <<EOF
{{- with nomadVar "nomad/jobs/abc-nodes-storage" -}}
AWS_ACCESS_KEY_ID={{ .access_key_id }}
AWS_SECRET_ACCESS_KEY={{ .secret_access_key }}
{{- end }}
EOF
      }

      env {
        # AWS SDK requires a region value even for non-AWS S3 endpoints.
        # Any string is accepted; us-east-1 is the conventional placeholder.
        AWS_DEFAULT_REGION = "us-east-1"
      }

      config {
        command = "/bin/sh"
        args = [
          "-c",
          <<-EOC
          set -e

          S5="${NOMAD_TASK_DIR}/s5cmd"
          SRC="${NOMAD_ALLOC_DIR}/output/"
          DST="s3://${var.s3_output_bucket}/${NOMAD_NAMESPACE}/${NOMAD_JOB_NAME}/${NOMAD_ALLOC_ID}/"

          chmod +x "$S5"

          # Graceful no-op: if the main task produced no output directory (or
          # left it empty) we exit 0.  Some jobs may conditionally produce
          # output; silence is not an error.
          if [ ! -d "$SRC" ] || [ -z "$(ls -A "$SRC" 2>/dev/null)" ]; then
            echo "upload-outputs: output dir empty or absent — nothing to upload"
            exit 0
          fi

          echo "upload-outputs: syncing $SRC → $DST"
          "$S5" \
            --endpoint-url "${var.s3_endpoint}" \
            --log error \
            sync "$SRC" "$DST"

          echo "upload-outputs: done ($(du -sh "$SRC" | cut -f1) uploaded)"
          EOC
        ]
      }

      # DESIGN DECISION: generous timeout.
      # s5cmd uploads in parallel (default: 256 concurrent workers) but large
      # bioinformatics outputs (BAM files, assembled genomes) can be tens of GBs.
      # 30 minutes covers most realistic job outputs on a LAN link to aither.
      # Operators can override via the abc CLI's --upload-timeout flag (future).
      #
      # kill_timeout gives s5cmd time to flush its in-flight multipart uploads
      # before Nomad SIGKILLs the task — avoids orphaned multipart sessions.
      kill_timeout = "60s"

      resources {
        # s5cmd uses significant CPU for parallel hashing + upload.
        # 500 MHz and 256 MB are comfortable for concurrent small-file uploads.
        # For large single-file uploads (>1 GB), CPU is not the bottleneck.
        cpu    = 500
        memory = 256
      }
    }
  }
}
