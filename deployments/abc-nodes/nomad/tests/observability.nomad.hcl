# Test: observability stack (Prometheus + Loki + Grafana)
#
# Tests:
#   Prometheus  — query API returns results; Nomad scrape target is up
#   Loki        — push a labelled log line; query it back; confirm round-trip
#   Grafana     — /api/health returns {database:ok}; datasources provisioned
#
# No credentials required (all endpoints are unauthenticated on the internal network).
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/observability.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

job "abc-nodes-test-observability" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "observability"
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

      # Grafana admin password — stored by store-test-secrets.sh
      template {
        destination = "secrets/grafana.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-test-observability" -}}
GRAFANA_ADMIN_PASSWORD={{ .grafana_admin_password }}
{{- else -}}
GRAFANA_ADMIN_PASSWORD=admin
{{- end }}
EOF
      }

      template {
        destination = "local/test.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
# abc-nodes observability stack test
set -u

HOST="${var.node_ip}"
PROM="http://$HOST:9090"
LOKI="http://$HOST:3100"
GRAFANA="http://$HOST:3000"
GRAFANA_PASS="$${GRAFANA_ADMIN_PASSWORD:-admin}"
PASS=0
FAIL=0

# Unique label value so the pushed log line is unambiguous when queried back
PROBE_ID="abc-nodes-obs-test-$$"

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

http_status() { curl -s -o /dev/null -w "%%{http_code}" --max-time 8 "$1" 2>/dev/null; }
http_body()   { curl -sL --max-time 8 "$1" 2>/dev/null; }

# POST with body, return HTTP status
post_status() {
  curl -s -o /dev/null -w "%%{http_code}" --max-time 8 \
    -X POST -H "Content-Type: application/json" \
    -d "$2" "$1" 2>/dev/null
}

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes observability stack test"
printf "  Prometheus: %s\n  Loki: %s\n  Grafana: %s\n" "$PROM" "$LOKI" "$GRAFANA"
echo "══════════════════════════════════════════════════════"

# ─── Prometheus ───────────────────────────────────────────────────────────────
echo ""
echo "  ── Prometheus ───────────────────────────────────────"

# Health check
body=$(http_body "$PROM/-/healthy")
echo "$body" | grep -qi "healthy" \
  && ok  "Prometheus /-/healthy" \
  || nok "Prometheus /-/healthy  →  unexpected: $body"

# Readiness check
body=$(http_body "$PROM/-/ready")
echo "$body" | grep -qi "ready" \
  && ok  "Prometheus /-/ready" \
  || nok "Prometheus /-/ready  →  unexpected: $body"

# Query: at least one 'up' time series must exist
up_result=$(http_body "$PROM/api/v1/query?query=up" 2>/dev/null)
up_count=$(echo "$up_result" | jq -r '.data.result | length' 2>/dev/null || echo "0")
[ "$up_count" -gt 0 ] \
  && ok  "Prometheus query 'up'  →  $up_count active targets" \
  || nok "Prometheus query 'up'  →  no results (scrape targets down?)"

# Query: Nomad self-metrics must be present (confirms Alloy → Prometheus pipeline)
nomad_result=$(http_body "$PROM/api/v1/query?query=nomad_runtime_num_goroutines" 2>/dev/null)
nomad_count=$(echo "$nomad_result" | jq -r '.data.result | length' 2>/dev/null || echo "0")
[ "$nomad_count" -gt 0 ] \
  && ok  "Prometheus has Nomad metrics  →  Alloy scrape pipeline working" \
  || nok "Prometheus missing Nomad metrics  →  check Alloy → Prometheus pipeline"

# Query: verify targets endpoint lists scrape jobs
targets_body=$(http_body "$PROM/api/v1/targets" 2>/dev/null)
active_count=$(echo "$targets_body" | jq -r '.data.activeTargets | length' 2>/dev/null || echo "0")
[ "$active_count" -gt 0 ] \
  && ok  "Prometheus /api/v1/targets  →  $active_count active scrape target(s)" \
  || nok "Prometheus /api/v1/targets  →  no active targets"

# ─── Loki ─────────────────────────────────────────────────────────────────────
echo ""
echo "  ── Loki ─────────────────────────────────────────────"

# Ready check
body=$(http_body "$LOKI/ready")
echo "$body" | grep -qi "ready" \
  && ok  "Loki /ready" \
  || nok "Loki /ready  →  unexpected: $body"

# Labels endpoint (confirms storage backend accessible)
labels_code=$(http_status "$LOKI/loki/api/v1/labels")
[ "$labels_code" = "200" ] \
  && ok  "Loki /loki/api/v1/labels  →  HTTP 200" \
  || nok "Loki /loki/api/v1/labels  →  HTTP $labels_code (storage backend issue?)"

# Push a test log line
now_ns=$(date +%s)000000000
push_payload='{"streams":[{"stream":{"job":"abc-nodes-test","probe_id":"'"$PROBE_ID"'"},"values":[["'"$now_ns"'","connectivity probe: '"$PROBE_ID"'"]]}]}'
push_code=$(post_status "$LOKI/loki/api/v1/push" "$push_payload")
[ "$push_code" = "204" ] \
  && ok  "Loki push log line  →  HTTP 204" \
  || nok "Loki push log line  →  HTTP $push_code (expected 204)"

# Brief pause to allow the log line to be ingested
sleep 3

# Query back the pushed line by probe_id label
query_url="$LOKI/loki/api/v1/query_range?query=%7Bjob%3D%22abc-nodes-test%22%2Cprobe_id%3D%22$PROBE_ID%22%7D&limit=5&start=$now_ns"
query_body=$(http_body "$query_url")
result_count=$(echo "$query_body" | jq -r '.data.result | length' 2>/dev/null || echo "0")
[ "$result_count" -gt 0 ] \
  && ok  "Loki query round-trip  →  pushed line retrieved" \
  || nok "Loki query round-trip  →  pushed line not found (ingestion delay or storage issue)"

# ─── Grafana ──────────────────────────────────────────────────────────────────
echo ""
echo "  ── Grafana ──────────────────────────────────────────"

# Health check
body=$(http_body "$GRAFANA/api/health")
echo "$body" | jq -e '."database" == "ok"' >/dev/null 2>&1 \
  && ok  "Grafana /api/health  →  database ok" \
  || nok "Grafana /api/health  →  unexpected: $body"

# Verify provisioned datasources exist
ds_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
  -u "admin:$GRAFANA_PASS" "$GRAFANA/api/datasources" 2>/dev/null)
case "$ds_code" in
  200)
    ds_body=$(curl -sL --max-time 5 -u "admin:$GRAFANA_PASS" "$GRAFANA/api/datasources" 2>/dev/null)
    ds_count=$(echo "$ds_body" | jq 'length' 2>/dev/null || echo "0")
    prom_ok=$(echo "$ds_body" | jq -r '[.[].type] | contains(["prometheus"])' 2>/dev/null || echo "false")
    loki_ok=$(echo "$ds_body" | jq -r '[.[].type] | contains(["loki"])' 2>/dev/null || echo "false")
    ok "Grafana /api/datasources  →  $ds_count datasource(s) found"
    [ "$prom_ok" = "true" ] \
      && ok  "Grafana has Prometheus datasource provisioned" \
      || nok "Grafana missing Prometheus datasource"
    [ "$loki_ok" = "true" ] \
      && ok  "Grafana has Loki datasource provisioned" \
      || nok "Grafana missing Loki datasource"
    ;;
  401)
    nok "Grafana /api/datasources  →  401 (default admin/admin creds wrong — password rotated?)"
    ;;
  *)
    nok "Grafana /api/datasources  →  HTTP $ds_code"
    ;;
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
        cpu    = 150
        memory = 128
      }
    }
  }
}
