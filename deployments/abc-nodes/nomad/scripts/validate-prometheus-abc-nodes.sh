#!/usr/bin/env bash
# validate-prometheus-abc-nodes.sh
#
# Smoke-test Prometheus after abc-nodes observability deploy: scrape health,
# presence of Nomad + MinIO metrics, and valid MHz→cores conversion PromQL.
#
# Usage (from repo root = analysis/packages/abc-cluster-cli):
#   bash deployments/abc-nodes/nomad/scripts/validate-prometheus-abc-nodes.sh
#
# Env:
#   PROMETHEUS_QUERY_BASE  Base URL for Prometheus HTTP API (default below).
set -euo pipefail

PROMETHEUS_QUERY_BASE="${PROMETHEUS_QUERY_BASE:-http://aither.mb.sun.ac.za/prometheus}"
PROMETHEUS_QUERY_BASE="${PROMETHEUS_QUERY_BASE%/}"

echo "==> Prometheus query base: ${PROMETHEUS_QUERY_BASE}"

query() {
  local promql="$1"
  curl -sfG "${PROMETHEUS_QUERY_BASE}/api/v1/query" --data-urlencode "query=${promql}"
}

check_json() {
  local label="$1"
  local promql="$2"
  local out
  if ! out="$(query "${promql}")"; then
    echo "ERROR: ${label}: HTTP request failed" >&2
    exit 1
  fi
  python3 -c '
import json, sys
label, raw = sys.argv[1], sys.argv[2]
d = json.loads(raw)
if d.get("status") != "success":
    err = (d.get("errorType") or "") + " " + (d.get("error") or "")
    print(f"ERROR: {label}: {err}", file=sys.stderr)
    sys.exit(1)
res = d.get("data", {}).get("result", [])
print(f"OK: {label}  series={len(res)}")
for s in res[:12]:
    m = s.get("metric", {})
    job = m.get("job", "")
    inst = m.get("instance", "")
    bucket = m.get("bucket", "")
    val = s.get("value", ["", ""])[1]
    if bucket:
        ident = f"bucket={bucket}"
    elif job or inst:
        ident = f"{job}@{inst}"
    else:
        ident = "(aggregate)"
    print(f"    {ident}  {val}")
if len(res) > 12:
    print(f"    ... ({len(res) - 12} more)")
' "$label" "$out"
}

check_json "up (key scrape jobs)" \
  'up{job=~"prometheus|nomad|minio|minio_bucket"}'

check_json "MinIO bucket metric present" \
  'sum by (bucket) (minio_bucket_usage_total_bytes)'

check_json "Nomad MHz→cores conversion (must parse)" \
  'sum(nomad_client_allocated_cpu * on(host) group_left() (count by (host) (nomad_client_host_cpu_total) / on(host) (nomad_client_allocated_cpu + nomad_client_unallocated_cpu)))'

echo "Done."
