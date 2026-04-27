# abc-backups.nomad.hcl
#
# Periodic restic-on-Garage backups of cluster state.
#
# WHAT GETS SNAPSHOTTED
# ─────────────────────
#  1. Consul KV + ACLs + service catalog (consul snapshot HTTP API)
#  2. Vault raft storage     (vault sys/storage/raft/snapshot API)
#  3. Nomad job submissions  (every job's spec via /v1/jobs + /v1/job/<id>)
#
#  Each is staged into the alloc working dir then committed to a single
#  restic snapshot named `cluster:<utcdate>` in the `cluster-backups` bucket.
#
# WHY restic-on-Garage
# ────────────────────
#  Restic gives encryption-at-rest, content-addressed dedup, and time-travel
#  versioning that Garage lacks.  Garage gives geo-replication + dedup at the
#  block layer.  CPU note: restic already zstds, Garage will zstd again at
#  the block layer for ~zero size gain — accepted because compression is
#  global in Garage (not per-bucket).  Burn is small for nightly backups.
#
# RETENTION
# ─────────
#  After each run: keep 7 daily, 4 weekly, 12 monthly snapshots.  prune
#  removes unreferenced data.  Tunable via the *_keep_* vars.
#
# CREDENTIALS
# ───────────
#  - RESTIC_PASSWORD: encryption key for the restic repo. MUST be backed up
#    out-of-band (e.g. team password manager).  Losing it loses the backups.
#  - Restic AK/SK: imported into Garage as `restic-key` by the garage bootstrap.
#  - Vault/Consul/Nomad tokens: required because the cluster has ACL enabled.
#    Operator-level capability needed (raft snapshot / consul snapshot / nomad
#    read-job).  Defaults are empty — Terraform should supply them.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "schedule_cron" {
  type        = string
  default     = "30 2 * * *"
  description = "Cron schedule (UTC). Default 02:30 daily."
}

# ── Garage / restic repo ─────────────────────────────────────────────────────
variable "garage_endpoint" {
  type    = string
  default = "http://abc-nodes-garage-s3.service.consul:3900"
}

variable "garage_restic_access_key" {
  type    = string
  default = "GKADMIN000000000000RESTIC"
}

variable "garage_restic_secret_key" {
  type      = string
  default   = "change-me-restic-secret-key-32chars-min"
}

variable "garage_backup_bucket" {
  type    = string
  default = "cluster-backups"
}

variable "restic_password" {
  type        = string
  description = "restic repo encryption key — back up OUT OF BAND."
  default     = "change-me-restic-password-please-rotate"
}

# ── Source endpoints (in-cluster) ────────────────────────────────────────────
variable "consul_addr" {
  type    = string
  default = "http://100.70.185.46:8500"
}

variable "consul_token" {
  type      = string
  default   = ""
}

variable "vault_addr" {
  type    = string
  default = "http://100.70.185.46:8200"
}

variable "vault_token" {
  type      = string
  default   = ""
}

variable "nomad_addr" {
  type    = string
  default = "http://100.70.185.46:4646"
}

variable "nomad_token" {
  type      = string
  default   = ""
}

# ── Retention ────────────────────────────────────────────────────────────────
variable "keep_daily" {
  type    = number
  default = 7
}

variable "keep_weekly" {
  type    = number
  default = 4
}

variable "keep_monthly" {
  type    = number
  default = 12
}

# ── ntfy ─────────────────────────────────────────────────────────────────────
variable "ntfy_url" {
  type    = string
  default = "http://ntfy.aither/backups"
}

job "abc-nodes-backups" {
  namespace   = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "batch"
  priority    = 70

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "abc-backups"
  }

  periodic {
    crons             = [var.schedule_cron]
    prohibit_overlap = true
    time_zone        = "UTC"
  }

  group "backup" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    reschedule {
      attempts  = 1
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

    task "backup" {
      driver = "containerd-driver"

      config {
        image      = "alpine:3.19"
        entrypoint = ["/bin/sh", "-c"]
        args       = ["${NOMAD_TASK_DIR}/run.sh"]
      }

      env {
        # restic repo location and creds
        RESTIC_REPOSITORY     = "s3:${var.garage_endpoint}/${var.garage_backup_bucket}"
        AWS_ACCESS_KEY_ID     = var.garage_restic_access_key
        AWS_SECRET_ACCESS_KEY = var.garage_restic_secret_key
        AWS_DEFAULT_REGION    = "garage"

        # source-cluster API endpoints + tokens (operator supplies tokens via tfvars)
        CONSUL_HTTP_ADDR  = var.consul_addr
        CONSUL_HTTP_TOKEN = var.consul_token
        VAULT_ADDR        = var.vault_addr
        VAULT_TOKEN       = var.vault_token
        NOMAD_ADDR        = var.nomad_addr
        NOMAD_TOKEN       = var.nomad_token

        KEEP_DAILY   = var.keep_daily
        KEEP_WEEKLY  = var.keep_weekly
        KEEP_MONTHLY = var.keep_monthly

        NTFY_URL = var.ntfy_url
      }

      template {
        destination = "secrets/restic-password"
        perms       = "0400"
        data        = var.restic_password
      }

      template {
        destination = "local/run.sh"
        perms       = "0755"
        data        = <<-SCRIPT
#!/bin/sh
set -u

# Tools: restic + jq + curl. Alpine community has all three.
apk add --no-cache --quiet restic curl jq >/dev/null

export RESTIC_PASSWORD_FILE=/secrets/restic-password
STAGE=/local/stage
mkdir -p "$STAGE"

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
PARTIALS=""
SNAPSHOTS=""

# ── Initialise repo if first run (idempotent: silent failure on already-init).
restic snapshots >/dev/null 2>&1 || restic init || {
  echo "[backup] restic init FAILED" >&2
  exit 1
}

# ── 1. Consul snapshot via HTTP API ─────────────────────────────────────────
if [ -n "$${CONSUL_HTTP_TOKEN:-}" ] || [ "$${CONSUL_HTTP_TOKEN:-x}" = "" ]; then
  echo "[backup] consul snapshot →" "$STAGE/consul.snap"
  H=""
  [ -n "$${CONSUL_HTTP_TOKEN:-}" ] && H="-H X-Consul-Token:$CONSUL_HTTP_TOKEN"
  if curl -sS --fail $H "$CONSUL_HTTP_ADDR/v1/snapshot" -o "$STAGE/consul.snap"; then
    echo "[backup] consul snapshot ok ($(wc -c < $STAGE/consul.snap) bytes)"
  else
    echo "[backup] consul snapshot FAILED — continuing without"
    rm -f "$STAGE/consul.snap"
    PARTIALS="$PARTIALS consul"
  fi
fi

# ── 2. Vault raft snapshot ──────────────────────────────────────────────────
if [ -n "$${VAULT_TOKEN:-}" ]; then
  echo "[backup] vault snapshot →" "$STAGE/vault.snap"
  if curl -sS --fail \
       -H "X-Vault-Token:$VAULT_TOKEN" \
       "$VAULT_ADDR/v1/sys/storage/raft/snapshot" -o "$STAGE/vault.snap"; then
    echo "[backup] vault snapshot ok ($(wc -c < $STAGE/vault.snap) bytes)"
  else
    echo "[backup] vault snapshot FAILED — continuing without"
    rm -f "$STAGE/vault.snap"
    PARTIALS="$PARTIALS vault"
  fi
else
  echo "[backup] VAULT_TOKEN unset — skipping vault snapshot"
  PARTIALS="$PARTIALS vault(no-token)"
fi

# ── 3. Nomad job submissions ────────────────────────────────────────────────
echo "[backup] nomad jobs → $STAGE/nomad-jobs/"
mkdir -p "$STAGE/nomad-jobs"
HN=""
[ -n "$${NOMAD_TOKEN:-}" ] && HN="-H X-Nomad-Token:$NOMAD_TOKEN"
if curl -sS --fail $HN "$NOMAD_ADDR/v1/jobs?namespace=*" \
     | jq -r '.[] | "\(.Namespace)/\(.ID)"' > "$STAGE/nomad-jobs/_index.txt"; then
  N=0
  while IFS= read -r line; do
    NS=$(echo "$line" | cut -d/ -f1)
    JID=$(echo "$line" | cut -d/ -f2)
    [ -z "$JID" ] && continue
    SAFE=$(echo "$NS-$JID" | tr -c 'A-Za-z0-9._-' '_')
    curl -sS --fail $HN \
      "$NOMAD_ADDR/v1/job/$JID?namespace=$NS" > "$STAGE/nomad-jobs/$SAFE.json" \
      || { rm -f "$STAGE/nomad-jobs/$SAFE.json"; continue; }
    N=$((N+1))
  done < "$STAGE/nomad-jobs/_index.txt"
  echo "[backup] nomad: captured $N jobs"
else
  echo "[backup] nomad list FAILED"
  PARTIALS="$PARTIALS nomad"
fi

# ── 4. restic backup ─────────────────────────────────────────────────────────
TAG="cluster:$(date -u +%Y%m%dT%H%M%SZ)"
echo "[backup] restic backup tag=$TAG"
if restic backup "$STAGE" \
     --tag "$TAG" \
     --host abc-cluster \
     --exclude '*.tmp' \
     --compression auto; then
  SNAPSHOT_ID=$(restic snapshots --tag "$TAG" --json 2>/dev/null \
    | jq -r '.[-1].short_id // empty')
  SNAPSHOTS="$TAG ($SNAPSHOT_ID)"
  echo "[backup] snapshot $SNAPSHOTS created"
else
  echo "[backup] restic backup FAILED"
  exit 2
fi

# ── 5. Retention prune ───────────────────────────────────────────────────────
echo "[backup] forget+prune (daily=$KEEP_DAILY weekly=$KEEP_WEEKLY monthly=$KEEP_MONTHLY)"
restic forget \
  --host abc-cluster \
  --keep-daily   "$KEEP_DAILY" \
  --keep-weekly  "$KEEP_WEEKLY" \
  --keep-monthly "$KEEP_MONTHLY" \
  --prune || echo "[backup] forget/prune had issues — continuing"

END=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ── 6. ntfy notify (best-effort) ─────────────────────────────────────────────
TOPIC=$(echo "$NTFY_URL" | sed 's|.*/||')
BASE=$(echo "$NTFY_URL"  | sed "s|/$${TOPIC}$||")
TITLE="abc-backups: ok"
PRIO=2
TAGS='["floppy_disk","white_check_mark"]'
if [ -n "$PARTIALS" ]; then
  TITLE="abc-backups: partial"; PRIO=4; TAGS='["floppy_disk","warning"]'
fi
MSG="start=$START\nend=$END\nsnapshot=$SNAPSHOTS\npartial:$${PARTIALS:- none}"
BODY=$(printf '{"topic":"%s","title":"%s","priority":%d,"tags":%s,"message":"%s"}' \
  "$TOPIC" "$TITLE" "$PRIO" "$TAGS" "$MSG")
curl -sS -H Content-Type:application/json -X POST -d "$BODY" "$BASE" >/dev/null 2>&1 || true

echo "[backup] done"
SCRIPT
      }

      resources {
        cpu    = 300
        memory = 384
      }
    }
  }
}
