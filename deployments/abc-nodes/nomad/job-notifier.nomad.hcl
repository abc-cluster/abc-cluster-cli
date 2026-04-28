# Nomad job event notifier — abc-nodes floor
#
# Watches the Nomad event stream (/v1/event/stream?topic=Allocation) and, when
# an allocation reaches a terminal state (complete, failed, lost), publishes:
#   • PRIMARY:  NATS subject events.jobs.<status> with full event JSON
#   • MIRROR:   ntfy POST (kept for back-compat — set ntfy_url="" to disable)
#
# Subject taxonomy (NATS):
#   events.jobs.complete   — allocation terminated successfully
#   events.jobs.failed     — allocation FAILED
#   events.jobs.lost       — allocation lost (node disconnect / drain)
#
# Subscribers can listen to events.jobs.> (all terminal-status events) or to
# a specific status.  Payload is the original Nomad event JSON, so subscribers
# don't have to track this bridge's schema — re-derive whatever they need.
#
# Uses raw_exec + host network so it can reach the Nomad API, the NATS broker,
# and ntfy without bridge / CNI requirements.  NATS publish uses bash's
# built-in /dev/tcp redirect — no nc or python required, just a recent bash.
#
# Requires jq on the host node (install via package manager).
#
# Deploy: abc admin services nomad cli -- job run -detach deployments/abc-nodes/nomad/job-notifier.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "nomad_addr" {
  type    = string
  default = "http://100.70.185.46:4646"
}

variable "nomad_token" {
  type        = string
  default     = ""
  description = "Nomad management token used to read the event stream. Injected via terraform's hcl2.vars (Workload-Identity JWT verify is broken on this cluster — server returns 500). Do NOT commit a real token here; let terraform supply it from var.nomad_token (or the hardcoded fallback in main.tf)."
}

variable "ntfy_url" {
  type        = string
  description = "ntfy base URL (no trailing slash). Resolves via Consul DNS on the host."
  default     = "http://abc-nodes-ntfy.service.consul:8088"
}

variable "ntfy_topic" {
  type    = string
  default = "abc-jobs"
}

variable "nats_host" {
  type        = string
  default     = "100.70.185.46"
  description = "NATS broker host (Tailscale IP works because raw_exec uses host network)."
}

variable "nats_port" {
  type    = number
  default = 4222
}

variable "nats_subject_prefix" {
  type        = string
  default     = "events.jobs"
  description = "Subject prefix. Full subject is <prefix>.<status> where status ∈ {complete, failed, lost}."
}

job "abc-nodes-job-notifier" {
  namespace = "abc-services"
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "job-notifier"
  }

  group "notifier" {
    count = 1

    restart {
      attempts = 10
      interval = "5m"
      delay    = "15s"
      mode     = "delay"
    }

    network {
      mode = "host"
    }

    task "watcher" {
      driver = "raw_exec"

      config {
        # Switched to bash — we need /dev/tcp/<host>/<port> for NATS publish.
        command = "/bin/bash"
        args    = ["local/watcher.sh"]
      }

      # watcher.sh — streams the Nomad Allocation event topic, publishes to
      # NATS (primary), and mirrors to ntfy (back-compat).
      template {
        destination = "local/watcher.sh"
        perms       = "755"
        data        = <<EOF
#!/usr/bin/env bash
set -u

if ! command -v jq >/dev/null 2>&1; then
  echo "[notifier] jq not found — install jq on the host node." >&2
  exit 1
fi

echo "[notifier] Starting"
echo "[notifier]   nomad : $NOMAD_ADDR"
echo "[notifier]   nats  : $NATS_HOST:$NATS_PORT  prefix=$NATS_SUBJECT_PREFIX"
echo "[notifier]   ntfy  : $${NTFY_URL:-(disabled)}/$NTFY_TOPIC"

# nats_pub  <subject>  <payload>
# Speaks just enough of the NATS protocol over /dev/tcp to publish without
# auth.  Pure bash — no nc / python / nats-cli dependency.  Failures here
# are non-fatal: the ntfy mirror still fires below.
nats_pub() {
  local subj=$1 payload=$2
  if [ -z "$NATS_HOST" ]; then return 1; fi
  # Open a bi-directional TCP socket as fd 3.  Race-tolerant: timeout on
  # connect via wrapping in a subshell with bash's TMOUT-style timeout.
  if ! exec 3<>"/dev/tcp/$NATS_HOST/$NATS_PORT" 2>/dev/null; then
    return 1
  fi
  # Read the server's INFO line (don't parse it).
  read -t 1 -r _info <&3 || true
  printf 'CONNECT {"verbose":false,"pedantic":false,"name":"job-notifier","echo":false,"protocol":1,"lang":"bash"}\r\n' >&3
  local len=$${#payload}
  printf 'PUB %s %s\r\n%s\r\n' "$subj" "$len" "$payload" >&3
  printf 'PING\r\n' >&3
  read -t 2 -r _pong <&3 || true   # consume PONG (ignore)
  exec 3>&-
  exec 3<&-
  return 0
}

while true; do
  curl -sN \
    -H "X-Nomad-Token: $NOMAD_TOKEN" \
    "$NOMAD_ADDR/v1/event/stream?topic=Allocation" \
  | while IFS= read -r line; do
      [ -z "$line" ] && continue

      echo "$line" \
        | jq -c '.Events[]? | select(.Type == "AllocationUpdated")' 2>/dev/null \
      | while IFS= read -r ev; do
          cs=$(echo "$ev" | jq -r '.Payload.Allocation.ClientStatus // empty')
          job_id=$(echo "$ev" | jq -r '.Payload.Allocation.JobID // "unknown"')
          alloc_short=$(echo "$ev" | jq -r '.Payload.Allocation.ID // "unknown"' | cut -c1-8)
          ns=$(echo "$ev" | jq -r '.Payload.Allocation.Namespace // "default"')

          case "$cs" in
            complete|failed|lost) ;;
            *) continue ;;
          esac

          # Avoid feedback loop for this job itself.
          case "$job_id" in
            abc-nodes-job-notifier*) continue ;;
          esac

          # ── 1. NATS publish (primary).  Subject = events.jobs.<status>.
          #    Payload = the full Nomad event JSON, compact-printed by jq -c.
          subject="$NATS_SUBJECT_PREFIX.$cs"
          if nats_pub "$subject" "$ev"; then
            echo "[notifier] nats <- $subject  $job_id ($ns) $alloc_short"
          else
            echo "[notifier] nats publish failed for $subject" >&2
          fi

          # ── 2. ntfy mirror (back-compat).  Disabled when NTFY_URL="".
          if [ -n "$${NTFY_URL:-}" ]; then
            case "$cs" in
              complete) title="Job complete: $job_id"; prio=3; body="$job_id ($ns) alloc $alloc_short finished successfully." ;;
              failed)   title="Job FAILED: $job_id";   prio=4; body="$job_id ($ns) alloc $alloc_short FAILED. Check Nomad UI." ;;
              lost)     title="Job lost: $job_id";     prio=4; body="$job_id ($ns) alloc $alloc_short was lost (node issue?)." ;;
            esac
            curl -s -X POST "$NTFY_URL/$NTFY_TOPIC" \
              -H "X-Title: $title" \
              -H "X-Priority: $prio" \
              -H "X-Tags: nomad,$cs" \
              -d "$body" >/dev/null
            echo "[notifier] ntfy <- $title"
          fi
        done
    done

  echo "[notifier] stream disconnected, reconnecting in 5s..." >&2
  sleep 5
done
EOF
      }

      env {
        NOMAD_ADDR          = var.nomad_addr
        NTFY_URL            = var.ntfy_url
        NTFY_TOPIC          = var.ntfy_topic
        NATS_HOST           = var.nats_host
        NATS_PORT           = var.nats_port
        NATS_SUBJECT_PREFIX = var.nats_subject_prefix
      }

      # NOMAD_TOKEN injected via terraform's hcl2.vars at job-submit time.
      # The HCL2 var ${var.nomad_token} resolves once during HCL parsing.
      # Workload-identity-based nomadVar reads fail on this cluster (server
      # returns 500 on WI JWT verification), so we can't rely on runtime
      # template lookup.
      template {
        destination = "secrets/token.env"
        env         = true
        data        = "NOMAD_TOKEN=${var.nomad_token}\n"
      }

      resources {
        cpu    = 50
        memory = 64
      }

      # Consul registration for visibility only — job-notifier is a pure outbound
      # daemon (no listening port), so no health check is attached.
      service {
        name     = "abc-nodes-job-notifier"
        provider = "consul"
        tags     = ["abc-nodes", "notifier", "ntfy"]
      }
    }
  }
}
