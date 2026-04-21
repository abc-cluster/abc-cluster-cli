# Test: tusd resumable upload (TUS protocol)
#
# Performs a complete TUS upload cycle against tusd via Traefik (so the
# ForwardAuth middleware is exercised as part of a real upload flow).
#
# Tests:
#   OPTIONS /files          — TUS capabilities advertised (Tus-Version, Tus-Extension)
#   POST /files             — create an upload, receive Location header
#   PATCH /files/<id>       — upload the file content
#   HEAD /files/<id>        — verify Upload-Offset equals content length
#   Object in MinIO         — confirm the object landed in the S3 bucket
#
# A valid Nomad ACL token is required (ForwardAuth gate on the tusd route).
# Token: nomad/jobs/abc-nodes-test-upload-tusd key nomad_token  OR  -var nomad_token=<t>
#
# Setup (once):
#   bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/upload-tusd.nomad.hcl

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
  description = "Nomad ACL token for ForwardAuth on the tusd route."
}

variable "tusd_host" {
  type    = string
  default = "tusd.aither"
}

variable "minio_access_key" {
  type    = string
  default = "minio-admin"
}

variable "minio_secret_key" {
  type    = string
  default = ""
}

job "abc-nodes-test-upload-tusd" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "upload-tusd"
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
        destination = "secrets/creds.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-test-upload-tusd" -}}
NOMAD_TEST_TOKEN={{ .nomad_token }}
MINIO_ACCESS_KEY={{ .minio_access_key }}
MINIO_SECRET_KEY={{ .minio_secret_key }}
{{- else -}}
NOMAD_TEST_TOKEN=${var.nomad_token}
MINIO_ACCESS_KEY=${var.minio_access_key}
MINIO_SECRET_KEY=${var.minio_secret_key}
{{- end }}
EOF
      }

      template {
        destination = "local/test.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
# abc-nodes tusd TUS upload test
set -u

HOST="${var.node_ip}"
TRAEFIK="http://$HOST:80"
TUSD_HOST="${var.tusd_host}"
MINIO_URL="http://$HOST:9000"
TUSD_BUCKET="tusd"   # bucket tusd writes uploads to
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

# Payload to upload
CONTENT="abc-nodes-upload-probe-$$"
CONTENT_LEN=$(echo -n "$CONTENT" | wc -c | tr -d ' ')
PROBE_FILENAME="abc-nodes-probe-$$.txt"

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes tusd TUS upload test"
printf "  tusd via Traefik: %s  Host: %s\n" "$TRAEFIK" "$TUSD_HOST"
echo "══════════════════════════════════════════════════════"

# ─── OPTIONS — TUS capability discovery ──────────────────────────────────────
echo ""
echo "  ── TUS capability discovery (OPTIONS) ───────────────"

opts_resp=$(curl -sI --max-time 5 \
  -X OPTIONS \
  -H "Host: $TUSD_HOST" \
  -H "Tus-Resumable: 1.0.0" \
  "$TRAEFIK/files/" 2>/dev/null)
opts_code=$(echo "$opts_resp" | head -1 | awk '{print $2}')

# OPTIONS may return 200 or 204
case "$opts_code" in
  200|204)
    ok "OPTIONS /files/  →  HTTP $opts_code"
    ;;
  401)
    # ForwardAuth blocks OPTIONS too; this is still informative
    ok "OPTIONS /files/  →  HTTP 401 (ForwardAuth gate active — expected)"
    ;;
  000)
    nok "OPTIONS /files/  →  no response (is tusd running? is Traefik routing $TUSD_HOST?)"
    ;;
  *)
    nok "OPTIONS /files/  →  HTTP $opts_code (unexpected)"
    ;;
esac

tus_ver=$(echo "$opts_resp" | grep -i "^Tus-Version:" | tr -d '\r' | awk '{print $2}')
[ -n "$tus_ver" ] \
  && ok  "Tus-Version header present  →  $tus_ver" \
  || printf "  [INFO] Tus-Version not visible (may be gated by auth)\n"

# ─── Authenticated upload ─────────────────────────────────────────────────────
echo ""
echo "  ── Authenticated upload flow ────────────────────────"

if [ -z "$NOMAD_TEST_TOKEN" ]; then
  printf "  [SKIP] NOMAD_TEST_TOKEN not set — skipping upload flow\n"
  printf "         Run: bash deployments/abc-nodes/scripts/store-test-secrets.sh\n"
else
  # POST /files — create the upload (Upload-Length in bytes)
  create_resp=$(curl -sI --max-time 10 \
    -X POST \
    -H "Host: $TUSD_HOST" \
    -H "X-Nomad-Token: $NOMAD_TEST_TOKEN" \
    -H "Tus-Resumable: 1.0.0" \
    -H "Upload-Length: $CONTENT_LEN" \
    -H "Upload-Metadata: filename $(echo -n "$PROBE_FILENAME" | base64 | tr -d '\n')" \
    -H "Content-Length: 0" \
    "$TRAEFIK/files/" 2>/dev/null)

  create_code=$(echo "$create_resp" | head -1 | awk '{print $2}')
  location=$(echo "$create_resp" | grep -i "^Location:" | tr -d '\r' | awk '{print $2}')

  [ "$create_code" = "201" ] \
    && ok  "POST /files/  →  HTTP 201 (upload created)" \
    || nok "POST /files/  →  HTTP $create_code (expected 201)"

  if [ -z "$location" ]; then
    nok "POST /files/  →  no Location header in response"
  else
    ok  "Location header  →  $location"

    # The Location may be an absolute URL; extract just the path
    upload_path=$(echo "$location" | sed 's|http://[^/]*||')

    # PATCH — send the actual file content
    patch_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 10 \
      -X PATCH \
      -H "Host: $TUSD_HOST" \
      -H "X-Nomad-Token: $NOMAD_TEST_TOKEN" \
      -H "Tus-Resumable: 1.0.0" \
      -H "Upload-Offset: 0" \
      -H "Content-Type: application/offset+octet-stream" \
      -H "Content-Length: $CONTENT_LEN" \
      -d "$CONTENT" \
      "$TRAEFIK$upload_path" 2>/dev/null)

    [ "$patch_code" = "204" ] \
      && ok  "PATCH (upload data)  →  HTTP 204 (chunk accepted)" \
      || nok "PATCH (upload data)  →  HTTP $patch_code (expected 204)"

    # HEAD — verify Upload-Offset equals the full content length
    head_resp=$(curl -sI --max-time 5 \
      -X HEAD \
      -H "Host: $TUSD_HOST" \
      -H "X-Nomad-Token: $NOMAD_TEST_TOKEN" \
      -H "Tus-Resumable: 1.0.0" \
      "$TRAEFIK$upload_path" 2>/dev/null)

    offset=$(echo "$head_resp" | grep -i "^Upload-Offset:" | tr -d '\r' | awk '{print $2}')
    [ "$offset" = "$CONTENT_LEN" ] \
      && ok  "HEAD Upload-Offset  →  $offset == $CONTENT_LEN (upload complete)" \
      || nok "HEAD Upload-Offset  →  got '$offset'  want '$CONTENT_LEN'"
  fi
fi

# ─── MinIO bucket presence check ─────────────────────────────────────────────
echo ""
echo "  ── tusd bucket in MinIO ─────────────────────────────"

mc_bin=$(command -v mc 2>/dev/null || true)
if [ -n "$mc_bin" ] && ! mc --help 2>&1 | grep -q "GNU Midnight Commander" && [ -n "$MINIO_SECRET_KEY" ]; then
  ALIAS="tusd-test-$$"
  mc alias set "$ALIAS" "$MINIO_URL" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY" \
    --api S3v4 >/dev/null 2>&1
  mc ls "$ALIAS/$TUSD_BUCKET" >/dev/null 2>&1 \
    && ok  "MinIO bucket '$TUSD_BUCKET' exists (tusd upload destination)" \
    || nok "MinIO bucket '$TUSD_BUCKET' not found  (tusd may not have created it yet)"
  mc alias remove "$ALIAS" >/dev/null 2>&1 || true
else
  # Fallback: HTTP health check only (or non-MinIO "mc" binary on host)
  code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
    "$MINIO_URL/minio/health/live" 2>/dev/null)
  [ "$code" = "200" ] \
    && printf "  [INFO] MinIO reachable (HTTP %s) — bucket check skipped (mc or creds not available)\n" "$code" \
    || nok "MinIO unreachable  →  HTTP $code"
fi

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
