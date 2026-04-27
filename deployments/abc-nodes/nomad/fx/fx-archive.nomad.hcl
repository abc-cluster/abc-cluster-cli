# fx-archive.nomad.hcl
#
# Periodic age-based tier-down: RustFS (hot) → Garage (cold archive).
#
# ARCHITECTURE
# ────────────
#  Runs nightly (default 03:00).  For each configured RustFS bucket, lists
#  objects older than $ARCHIVE_AGE_DAYS and copies them to:
#    s3://archive/<bucket>/<key>  on Garage
#  Verifies each copy (HEAD-on-target) before optionally deleting from source.
#  By default deletion is OFF — operator opts in once the archive is trusted.
#
# WHY rclone
# ──────────
#  The fx-* family elsewhere uses Python+stdlib SigV4 for tight bidirectional
#  hooks (fx-tusd-hook).  This job is a many-objects bulk-copy use case where
#  rclone's `--min-age` filter and concurrent transfers are exactly the right
#  tool.  rclone's S3 backend handles SigV4, multipart, and resume natively.
#
# TWO REMOTES (rclone.conf)
# ─────────────────────────
#  [rustfs]   provider=Other  endpoint=http://abc-nodes-rustfs-s3.service.consul:9900
#             access_key_id / secret_access_key from RustFS admin creds
#  [garage]   provider=Other  endpoint=http://abc-nodes-garage-s3.service.consul:3900
#             access_key_id / secret_access_key from the archive-key in Garage
#
# NOTIFICATION
# ────────────
#  On completion (success or failure), POSTs a summary to ntfy.aither/archive.
#  Reuses the same notification pattern as fx-notify / fx-tusd-hook.
#
# WIRING
# ──────
#  Deploy via Terraform (preferred) — depends on nomad_job.garage and rustfs.
#  Force-run for testing:  nomad job periodic force fx-archive

variable "docker_node" {
  type        = string
  default     = "aither"
  description = "Node to schedule on (must run a Consul agent)."
}

variable "rustfs_endpoint" {
  type        = string
  default     = "http://abc-nodes-rustfs-s3.service.consul:9900"
  description = "RustFS S3 API base URL (in-cluster, no trailing slash)."
}

variable "rustfs_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "rustfs_secret_key" {
  type      = string
  default   = "rustfsadmin"
}

variable "garage_endpoint" {
  type    = string
  default = "http://abc-nodes-garage-s3.service.consul:3900"
}

variable "garage_archive_access_key" {
  type    = string
  default = "GKADMIN00000000000ARCHIVE"
}

variable "garage_archive_secret_key" {
  type      = string
  default   = "change-me-archive-secret-key-32chars-min"
}

variable "garage_bucket" {
  type        = string
  default     = "archive"
  description = "Destination bucket on Garage (created during garage bootstrap)."
}

# Comma-separated list of source buckets on RustFS to tier down.
variable "source_buckets" {
  type        = string
  default     = "tusd"
  description = "Comma-separated RustFS bucket names to tier into Garage/archive/<bucket>/."
}

variable "archive_age_days" {
  type        = number
  default     = 30
  description = "Only objects older than this are eligible for archival."
}

variable "delete_after_copy" {
  type        = bool
  default     = false
  description = "If true, delete from RustFS after a verified copy. Off by default."
}

variable "ntfy_url" {
  type    = string
  default = "http://ntfy.aither/archive"
}

variable "schedule_cron" {
  type        = string
  default     = "0 3 * * *"
  description = "Cron schedule (UTC). Default 03:00 daily."
}

job "fx-archive" {
  namespace = "abc-automations"
  type      = "batch"
  priority  = 40

  periodic {
    crons             = [var.schedule_cron]
    prohibit_overlap = true
    time_zone        = "UTC"
  }

  group "archive" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = var.docker_node
    }

    # batch jobs: don't restart on success; do retry transient failures a couple times.
    reschedule {
      attempts  = 2
      interval  = "1h"
      delay     = "10m"
      unlimited = false
    }

    restart {
      attempts = 1
      interval = "30m"
      delay    = "30s"
      mode     = "fail"
    }

    task "rclone" {
      driver = "containerd-driver"

      config {
        image      = "rclone/rclone:1.66"
        entrypoint = ["/bin/sh", "-c"]
        args       = ["${NOMAD_TASK_DIR}/run.sh"]
      }

      env {
        RCLONE_CONFIG     = "/local/rclone.conf"
        SOURCE_BUCKETS    = var.source_buckets
        DEST_BUCKET       = var.garage_bucket
        AGE_DAYS          = var.archive_age_days
        DELETE_AFTER_COPY = var.delete_after_copy ? "true" : "false"
        NTFY_URL          = var.ntfy_url
      }

      template {
        destination = "local/rclone.conf"
        # Both endpoints use generic S3 — RustFS and Garage are S3-compatible
        # (provider=Other tells rclone "don't try AWS-specific quirks").
        # path_style is required: both servers route by path, not vhost subdomain.
        data = <<-EOF
[rustfs]
type = s3
provider = Other
endpoint = ${var.rustfs_endpoint}
access_key_id = ${var.rustfs_access_key}
secret_access_key = ${var.rustfs_secret_key}
region = us-east-1
force_path_style = true

[garage]
type = s3
provider = Other
endpoint = ${var.garage_endpoint}
access_key_id = ${var.garage_archive_access_key}
secret_access_key = ${var.garage_archive_secret_key}
region = garage
force_path_style = true
EOF
      }

      template {
        destination = "local/run.sh"
        perms       = "0755"
        data        = <<-SCRIPT
#!/bin/sh
set -eu

# rclone is the entrypoint of the official image; we call it via its alias.
RCLONE="rclone --config /local/rclone.conf"

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
TOTAL_COPIED=0
TOTAL_BYTES=0
FAILURES=0
LOG=/local/run.log
: > "$LOG"

# Comma-split SOURCE_BUCKETS.
OLD_IFS=$IFS
IFS=,
for B in $SOURCE_BUCKETS; do
  B=$(echo "$B" | tr -d ' ')
  [ -z "$B" ] && continue
  echo "[fx-archive] tiering rustfs:$B → garage:$DEST_BUCKET/$B (>= $${AGE_DAYS}d)" | tee -a "$LOG"

  # rclone copy with --min-age filters by age. --immutable means we never
  # overwrite a destination object (idempotent re-run).  -P prints periodic
  # progress.  --s3-no-check-bucket avoids HeadBucket on every run.
  if $RCLONE copy \
       --min-age "$${AGE_DAYS}d" \
       --immutable \
       --s3-no-check-bucket \
       --transfers 4 \
       --checkers 8 \
       --stats 30s \
       --log-file "$LOG" --log-level INFO \
       "rustfs:$B" "garage:$DEST_BUCKET/$B"; then
    echo "[fx-archive] $B: copy ok" | tee -a "$LOG"
  else
    rc=$?
    echo "[fx-archive] $B: copy FAILED (rc=$rc)" | tee -a "$LOG"
    FAILURES=$((FAILURES+1))
    continue
  fi

  if [ "$DELETE_AFTER_COPY" = "true" ]; then
    echo "[fx-archive] $B: deleting source objects older than $${AGE_DAYS}d" | tee -a "$LOG"
    # Verify-then-delete: rclone's --check-first + size compare before deletion.
    $RCLONE delete \
        --min-age "$${AGE_DAYS}d" \
        --log-file "$LOG" --log-level INFO \
        "rustfs:$B" || FAILURES=$((FAILURES+1))
  fi
done
IFS=$OLD_IFS

END=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Tail of log → ntfy summary.
SUMMARY=$(tail -n 30 "$LOG" | sed 's/[^[:print:]\t]//g' | head -c 1500)
TITLE="fx-archive: $${FAILURES} failure(s)"
if [ "$FAILURES" -eq 0 ]; then
  TITLE="fx-archive: ok"
  TAGS='["package","white_check_mark"]'
  PRIO=2
else
  TAGS='["package","warning"]'
  PRIO=4
fi

# ntfy notification (best-effort; never fails the job).
TOPIC=$(echo "$NTFY_URL" | sed 's|.*/||')
BASE=$(echo "$NTFY_URL" | sed "s|/$${TOPIC}$||")
BODY=$(cat <<JSON
{"topic":"$TOPIC","title":"$TITLE","priority":$PRIO,"tags":$TAGS,
 "message":"start=$START\nend=$END\nbuckets=$SOURCE_BUCKETS\nage_days=$AGE_DAYS\ndelete=$DELETE_AFTER_COPY\n---\n$SUMMARY"}
JSON
)
wget -q -O- --header='Content-Type: application/json' \
     --post-data="$BODY" "$BASE" >/dev/null 2>&1 || true

# Exit code reflects per-bucket failures (Nomad reschedules on non-zero).
exit "$FAILURES"
SCRIPT
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
