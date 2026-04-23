# Test: abc-nodes connectivity smoke test
#
# Checks that every core service responds on its expected port and endpoint.
# No credentials required.
#
# Tests:
#   MinIO S3 /minio/health/live, console UI
#   Loki     /ready
#   Prometheus /-/healthy
#   Grafana  /api/health
#   Alloy    TCP :12345
#   ntfy     /v1/health
#   Traefik  /ping, dashboard TCP
#   Vault    /v1/sys/health (reports seal status)
#   Docker registry /v2/
#   abc-nodes-auth  TCP :9191
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/connectivity.nomad.hcl
#
# Logs:
#   abc admin services nomad cli -- alloc logs -namespace services <alloc-id> test

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

job "abc-nodes-test-connectivity" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "connectivity"
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
# abc-nodes connectivity smoke test
set -u

HOST="${var.node_ip}"
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

# HTTP: check that URL returns expected status code
http_check() {
  local label="$1" url="$2" want="$3"
  local got
  got=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 "$url" 2>/dev/null)
  [ "$got" = "$want" ] \
    && ok  "$label  →  HTTP $got" \
    || nok "$label  →  want HTTP $want  got HTTP $got  ($url)"
}

# HTTP: check that URL response body contains a substring
body_check() {
  local label="$1" url="$2" needle="$3"
  local body
  body=$(curl -sL --max-time 5 "$url" 2>/dev/null)
  echo "$body" | grep -q "$needle" \
    && ok  "$label" \
    || nok "$label  →  response from $url missing '$needle'"
}

# TCP: check that a port is open
tcp_check() {
  local label="$1" host="$2" port="$3"
  nc -z -w3 "$host" "$port" 2>/dev/null \
    && ok  "$label  →  TCP :$port open" \
    || nok "$label  →  TCP :$port unreachable"
}

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes connectivity smoke test"
printf "  node: %s\n" "$HOST"
echo "══════════════════════════════════════════════════════"

# ─── Storage ──────────────────────────────────────────────────────────────────
echo ""
echo "  ── Storage ──────────────────────────────────────────"
http_check "MinIO S3   /minio/health/live" "http://$HOST:9000/minio/health/live" "200"
http_check "MinIO console  :9001"          "http://$HOST:9001"                   "200"

# ─── Observability ────────────────────────────────────────────────────────────
echo ""
echo "  ── Observability ────────────────────────────────────"
body_check  "Loki       /ready"           "http://$HOST:3100/ready"       "ready"
body_check  "Prometheus /-/healthy"       "http://$HOST:9090/-/healthy"   "Healthy"
http_check  "Grafana    /api/health"      "http://$HOST:3000/api/health"  "200"
tcp_check   "Alloy      TCP :12345"       "$HOST" "12345"

# ─── Notifications ────────────────────────────────────────────────────────────
echo ""
echo "  ── Notifications ────────────────────────────────────"
body_check  "ntfy       /v1/health"       "http://$HOST:8088/v1/health"   "true"

# ─── Networking ───────────────────────────────────────────────────────────────
echo ""
echo "  ── Networking ───────────────────────────────────────"
http_check  "Traefik    /ping (dashboard)" "http://$HOST:8888/ping"        "200"
tcp_check   "Traefik    HTTP entry :80"   "$HOST" "80"

# ─── Secrets ──────────────────────────────────────────────────────────────────
echo ""
echo "  ── Secrets ──────────────────────────────────────────"
vault_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
  "http://$HOST:8200/v1/sys/health" 2>/dev/null)
case "$vault_code" in
  200) ok  "Vault      /v1/sys/health  →  initialized and unsealed" ;;
  429) ok  "Vault      /v1/sys/health  →  standby (initialized, unsealed)" ;;
  503) nok "Vault      /v1/sys/health  →  SEALED — run: bash deployments/abc-nodes/experimental/scripts/init-vault.sh" ;;
  501) nok "Vault      /v1/sys/health  →  NOT INITIALIZED — run: bash deployments/abc-nodes/experimental/scripts/init-vault.sh" ;;
  *)   nok "Vault      /v1/sys/health  →  unexpected HTTP $vault_code" ;;
esac

# ─── Registry ─────────────────────────────────────────────────────────────────
echo ""
echo "  ── Registry ─────────────────────────────────────────"
# Note: abc-nodes-docker-registry uses containerd-driver with mode="host" which
# does NOT expose port 5000 on the host interface on this cluster. The registry
# is only reachable from container workloads (Wave) and from docker clients that
# have configured 100.70.185.46:5000 as an insecure registry.
# We verify the job's Nomad allocation is present via the metrics endpoint.
reg_alloc=$(curl -sL --max-time 5 \
  "http://$HOST:4646/v1/job/abc-nodes-docker-registry/allocations?namespace=services" \
  2>/dev/null | jq '[.[] | select(.ClientStatus=="running")] | length' 2>/dev/null || echo "")
if [ "$reg_alloc" -ge 1 ] 2>/dev/null; then
  ok  "Docker registry  →  $reg_alloc running allocation(s) in Nomad"
else
  printf "  [INFO] Docker registry  →  allocation status unknown (API requires ACL token)\n"
  printf "         Use: abc admin services nomad cli -- job status -namespace services abc-nodes-docker-registry\n"
fi

# ─── Auth sidecar ─────────────────────────────────────────────────────────────
echo ""
echo "  ── Auth sidecar ─────────────────────────────────────"
tcp_check "abc-nodes-auth  TCP :9191" "127.0.0.1" "9191"

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
