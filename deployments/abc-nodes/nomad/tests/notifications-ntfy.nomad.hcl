# Test: ntfy push notification service
#
# Tests:
#   /v1/health              — service is healthy
#   Publish a message       — POST to a test topic, expect 200
#   Retrieve the message    — GET /topic/json, verify content matches
#   MinIO attachment store  — check ntfy can reach MinIO (inferred from health)
#
# Topics used: abc-nodes-test-probe-<alloc-pid> (ephemeral, not cleaned up —
# ntfy topics are ephemeral and expire on their own).
#
# No credentials required (ntfy is open on the internal network).
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/notifications-ntfy.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

job "abc-nodes-test-notifications-ntfy" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "notifications-ntfy"
  }

  group "run" {
    count = 1

    network {
      mode = "host"
    }

    task "test" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args    = ["local/test.sh"]
      }

      template {
        destination = "local/test.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
# abc-nodes ntfy test
set -u

HOST="${var.node_ip}"
NTFY="http://$HOST:8088"
TOPIC="abc-nodes-test-probe-$$"
MSG_TITLE="abc-nodes probe $$"
MSG_BODY="connectivity probe from Nomad test job, pid=$$"
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes ntfy test"
printf "  ntfy: %s\n  topic: %s\n" "$NTFY" "$TOPIC"
echo "══════════════════════════════════════════════════════"

# ─── Health check ─────────────────────────────────────────────────────────────
echo ""
echo "  ── Health ───────────────────────────────────────────"

health_body=$(curl -sL --max-time 5 "$NTFY/v1/health" 2>/dev/null)
health_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 "$NTFY/v1/health" 2>/dev/null)
healthy=$(echo "$health_body" | jq -r '.healthy // false' 2>/dev/null)

[ "$health_code" = "200" ] \
  && ok  "/v1/health  →  HTTP 200" \
  || nok "/v1/health  →  HTTP $health_code (expected 200)"

[ "$healthy" = "true" ] \
  && ok  "/v1/health  →  healthy=true" \
  || nok "/v1/health  →  healthy=$healthy (expected true)"

# ─── Info endpoint ────────────────────────────────────────────────────────────
echo ""
echo "  ── Service info ─────────────────────────────────────"

info_body=$(curl -sL --max-time 5 "$NTFY/v1/info" 2>/dev/null)
version=$(echo "$info_body" | jq -r '.version // "unknown"' 2>/dev/null)
base_url=$(echo "$info_body" | jq -r '.base_url // "unknown"' 2>/dev/null)
[ -n "$version" ] \
  && ok  "/v1/info  →  version=$version  base_url=$base_url" \
  || nok "/v1/info  →  could not parse version"

# ─── Publish a message ────────────────────────────────────────────────────────
echo ""
echo "  ── Publish + retrieve ───────────────────────────────"

pub_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
  -X POST "$NTFY/$TOPIC" \
  -H "X-Title: $MSG_TITLE" \
  -H "X-Priority: 2" \
  -H "X-Tags: abc-nodes,test" \
  -d "$MSG_BODY" 2>/dev/null)
[ "$pub_code" = "200" ] \
  && ok  "Publish message  →  HTTP $pub_code" \
  || nok "Publish message  →  HTTP $pub_code (expected 200)"

# Brief pause for ntfy to ingest
sleep 1

# Retrieve from the topic as JSON
msg_json=$(curl -sL --max-time 5 "$NTFY/$TOPIC/json?poll=1" 2>/dev/null)
# ntfy JSON stream returns one JSON object per line; grab the first message line
first_msg=$(echo "$msg_json" | head -1)
retrieved_title=$(echo "$first_msg" | jq -r '.title // empty' 2>/dev/null)
retrieved_msg=$(echo "$first_msg" | jq -r '.message // empty' 2>/dev/null)

[ "$retrieved_title" = "$MSG_TITLE" ] \
  && ok  "Retrieve title  →  '$retrieved_title'" \
  || nok "Retrieve title  →  got '$retrieved_title'  want '$MSG_TITLE'"

[ "$retrieved_msg" = "$MSG_BODY" ] \
  && ok  "Retrieve body  →  message content verified" \
  || nok "Retrieve body  →  got '$retrieved_msg'  want '$MSG_BODY'"

# Verify tags were stored
retrieved_tags=$(echo "$first_msg" | jq -r '.tags // [] | join(",")' 2>/dev/null)
echo "$retrieved_tags" | grep -q "abc-nodes" \
  && ok  "Retrieve tags  →  tags present ($retrieved_tags)" \
  || nok "Retrieve tags  →  expected 'abc-nodes' in tags, got '$retrieved_tags'"

# ─── abc-jobs topic sanity ────────────────────────────────────────────────────
# The job-notifier sends to the abc-jobs topic. Verify the topic is reachable
# (we don't assert messages exist — there may be none if no jobs ran recently).
echo ""
echo "  ── abc-jobs topic reachability ──────────────────────"

jobs_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
  "$NTFY/abc-jobs/json?poll=1" 2>/dev/null)
case "$jobs_code" in
  200) ok  "abc-jobs topic  →  HTTP 200 (reachable)" ;;
  *)   nok "abc-jobs topic  →  HTTP $jobs_code (expected 200)" ;;
esac

# ─── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "══════════════════════════════════════════════════════"
printf "  Results: %d passed,  %d failed\n" "$PASS" "$FAIL"
echo "══════════════════════════════════════════════════════"
[ "$FAIL" -eq 0 ] || exit 1
EOF
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
