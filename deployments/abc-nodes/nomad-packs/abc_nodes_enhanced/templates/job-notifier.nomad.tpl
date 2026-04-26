# Nomad job event notifier — abc-nodes floor

job "abc-nodes-job-notifier" {
  namespace   = [[ var "abc_services_namespace" . | quote ]]
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
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
        command = "/bin/sh"
        args    = ["local/watcher.sh"]
      }

      template {
        destination = "local/watcher.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
set -u

if ! command -v jq >/dev/null 2>&1; then
  echo "[notifier] jq not found — install jq on the host node." >&2
  exit 1
fi

echo "[notifier] Starting — Nomad: $NOMAD_ADDR  ntfy: $NTFY_URL/$NTFY_TOPIC"

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
            complete)
              title="Job complete: $job_id"
              prio=3
              body="$job_id ($ns) alloc $alloc_short finished successfully."
              ;;
            failed)
              title="Job FAILED: $job_id"
              prio=4
              body="$job_id ($ns) alloc $alloc_short FAILED. Check Nomad UI."
              ;;
            lost)
              title="Job lost: $job_id"
              prio=4
              body="$job_id ($ns) alloc $alloc_short was lost (node issue?)."
              ;;
            *) continue ;;
          esac

          case "$job_id" in
            abc-nodes-job-notifier*) continue ;;
          esac

          curl -s -X POST "$NTFY_URL/$NTFY_TOPIC" \
            -H "X-Title: $title" \
            -H "X-Priority: $prio" \
            -H "X-Tags: nomad,$cs" \
            -d "$body" >/dev/null
          echo "[notifier] sent: $title"
        done
    done

  echo "[notifier] stream disconnected, reconnecting in 5s..." >&2
  sleep 5
done
EOF
      }

      env {
        NOMAD_ADDR = [[ var "job_notifier_nomad_addr" . | quote ]]
        NTFY_URL   = [[ var "job_notifier_ntfy_url" . | quote ]]
        NTFY_TOPIC = [[ var "job_notifier_ntfy_topic" . | quote ]]
      }

      template {
        destination = "secrets/token.env"
        env         = true
        data        = <<EOF
    NOMAD_TOKEN=[[ var "job_notifier_nomad_token" . | quote ]]
EOF
      }

      resources {
        cpu    = 50
        memory = 64
      }

      service {
        name     = "abc-nodes-job-notifier"
        provider = "nomad"
        tags     = ["abc-nodes", "notifier", "ntfy"]
      }
    }
  }
}
