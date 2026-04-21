# Test: ForwardAuth middleware and abc-nodes-auth sidecar
#
# Verifies that the Traefik nomad-auth ForwardAuth middleware correctly
# gates protected routes and that abc-nodes-auth validates tokens properly.
#
# Tests:
#   Unauthenticated request to tusd via Traefik  →  401 Unauthorized
#   Invalid token to tusd via Traefik            →  401 Unauthorized
#   Direct call to /auth with no token           →  401 Unauthorized
#   Direct call to /auth with valid token        →  200 + X-Auth-User header
#   Valid-token request reaches tusd             →  not 401 (any other status is ok)
#
# Requires a valid Nomad ACL token in nomad/jobs/abc-nodes-test-auth-forwardauth (key: nomad_token)
# or passed as -var nomad_token=<token>.
#
# Setup (once):
#   bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/auth-forwardauth.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

variable "nomad_token" {
  type        = string
  default     = ""
  description = "Valid Nomad ACL token for auth verification. Override via -var or Nomad Variable."
}

variable "tusd_host" {
  type    = string
  default = "tusd.aither"
  description = "Virtual hostname used in Host: header for tusd routing through Traefik"
}

job "abc-nodes-test-auth-forwardauth" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "auth-forwardauth"
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

      # Inject Nomad token from Nomad Variable at runtime.
      # Falls back to the HCL variable default (empty string = skip token tests).
      template {
        destination = "secrets/auth.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-test-auth-forwardauth" -}}
NOMAD_TEST_TOKEN={{ .nomad_token }}
{{- else -}}
NOMAD_TEST_TOKEN=${var.nomad_token}
{{- end }}
EOF
      }

      template {
        destination = "local/test.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
# abc-nodes ForwardAuth + abc-nodes-auth test
set -u

HOST="${var.node_ip}"
TRAEFIK="http://$HOST:80"
AUTH_SIDECAR="http://127.0.0.1:9191"
TUSD_HOST="${var.tusd_host}"
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

http_code_with_headers() {
  # Returns just the HTTP status code for a GET with optional extra curl args
  curl -s -o /dev/null -w "%%{http_code}" --max-time 5 "$@" 2>/dev/null
}

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes ForwardAuth + abc-nodes-auth test"
printf "  Traefik: %s  Host: %s\n" "$TRAEFIK" "$TUSD_HOST"
printf "  Auth sidecar: %s\n" "$AUTH_SIDECAR"
echo "══════════════════════════════════════════════════════"

# ─── abc-nodes-auth sidecar ───────────────────────────────────────────────────
echo ""
echo "  ── abc-nodes-auth sidecar (/auth endpoint) ──────────"

# No token → must return 401
code=$(http_code_with_headers "$AUTH_SIDECAR/auth")
[ "$code" = "401" ] \
  && ok  "/auth with no token  →  401 (correctly denied)" \
  || nok "/auth with no token  →  HTTP $code (expected 401)"

# Garbage token → must return 401
code=$(http_code_with_headers -H "X-Nomad-Token: not-a-real-token-00000000" \
  "$AUTH_SIDECAR/auth")
[ "$code" = "401" ] \
  && ok  "/auth with invalid token  →  401 (correctly denied)" \
  || nok "/auth with invalid token  →  HTTP $code (expected 401)"

# ─── Traefik ForwardAuth gate ─────────────────────────────────────────────────
echo ""
echo "  ── Traefik nomad-auth middleware (via Host: $TUSD_HOST) ──"

# No token through Traefik → 401
code=$(http_code_with_headers -H "Host: $TUSD_HOST" "$TRAEFIK/")
[ "$code" = "401" ] \
  && ok  "Traefik tusd route, no token  →  401 (ForwardAuth active)" \
  || nok "Traefik tusd route, no token  →  HTTP $code (expected 401 — ForwardAuth may be missing)"

# Invalid token through Traefik → 401
code=$(http_code_with_headers -H "Host: $TUSD_HOST" \
  -H "X-Nomad-Token: aaaaaaaa-bbbb-cccc-dddd-000000000000" \
  "$TRAEFIK/")
[ "$code" = "401" ] \
  && ok  "Traefik tusd route, invalid token  →  401 (ForwardAuth rejecting bad tokens)" \
  || nok "Traefik tusd route, invalid token  →  HTTP $code (expected 401)"

# ─── Valid-token path (requires NOMAD_TEST_TOKEN) ─────────────────────────────
echo ""
echo "  ── Valid token path ─────────────────────────────────"

if [ -z "$NOMAD_TEST_TOKEN" ]; then
  printf "  [SKIP] NOMAD_TEST_TOKEN not set — skipping authenticated tests\n"
  printf "         Run: bash deployments/abc-nodes/scripts/store-test-secrets.sh\n"
else
  # Valid token to auth sidecar → 200 with X-Auth-User header
  auth_resp=$(curl -si --max-time 5 \
    -H "X-Nomad-Token: $NOMAD_TEST_TOKEN" \
    "$AUTH_SIDECAR/auth" 2>/dev/null)
  auth_code=$(echo "$auth_resp" | head -1 | awk '{print $2}')
  [ "$auth_code" = "200" ] \
    && ok  "/auth with valid token  →  200" \
    || nok "/auth with valid token  →  HTTP $auth_code (expected 200)"

  # Check that X-Auth-User header is present in the response
  echo "$auth_resp" | grep -qi "x-auth-user" \
    && ok  "/auth response has X-Auth-User header" \
    || nok "/auth response missing X-Auth-User header"

  # Valid token through Traefik → must NOT be 401 (actual status depends on tusd)
  code=$(http_code_with_headers -H "Host: $TUSD_HOST" \
    -H "X-Nomad-Token: $NOMAD_TEST_TOKEN" \
    "$TRAEFIK/")
  [ "$code" != "401" ] \
    && ok  "Traefik tusd route, valid token  →  HTTP $code (ForwardAuth passed through)" \
    || nok "Traefik tusd route, valid token  →  401 (valid token should not be denied)"
fi

# ─── Unprotected route sanity check ───────────────────────────────────────────
echo ""
echo "  ── Unprotected route sanity check ───────────────────"

# ntfy is not protected by nomad-auth — should respond without a token
code=$(http_code_with_headers -H "Host: ntfy.aither" "$TRAEFIK/v1/health")
[ "$code" = "200" ] \
  && ok  "Traefik ntfy route (unprotected)  →  HTTP $code (ForwardAuth not applied)" \
  || nok "Traefik ntfy route (unprotected)  →  HTTP $code (expected 200)"

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
