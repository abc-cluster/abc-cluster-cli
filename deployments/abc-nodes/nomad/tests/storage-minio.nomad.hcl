# Test: MinIO storage CRUD
#
# Verifies MinIO credentials work, required buckets exist, and the full
# object lifecycle (create bucket → upload → download → verify → delete) works.
#
# Requires mc (MinIO client) on the host.
# Credentials are read from Nomad Variable nomad/jobs/abc-nodes-test-storage-minio
# (populated by deployments/abc-nodes/scripts/store-test-secrets.sh).
#
# Tests:
#   Credential authentication via mc alias set
#   Bucket existence: loki, ntfy (created by other services)
#   Object round-trip: PUT → GET → content match → DELETE
#   Bucket cleanup
#
# Setup (once):
#   bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/nomad/tests/storage-minio.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

variable "minio_access_key" {
  type    = string
  default = "minio-admin"
}

variable "minio_secret_key" {
  type        = string
  default     = ""
  description = "Fallback only — override via Nomad Variable nomad/jobs/abc-nodes-test-storage-minio"
}

job "abc-nodes-test-storage-minio" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "storage-minio"
  }

  group "run" {
    count = 1

    network {
      mode = "host"
    }

    # Inject MinIO credentials from Nomad Variable at runtime.
    # Falls back to HCL variable defaults (useful for -var flag overrides).
    task "test" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args    = ["local/test.sh"]
      }

      template {
        destination = "secrets/minio.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-test-storage-minio" -}}
MINIO_ACCESS_KEY={{ .minio_access_key }}
MINIO_SECRET_KEY={{ .minio_secret_key }}
{{- else -}}
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
# abc-nodes MinIO storage CRUD test
set -u

HOST="${var.node_ip}"
MINIO_URL="http://$HOST:9000"
ALIAS="abc-nodes-test-$$"
BUCKET="abc-nodes-test-$$"
OBJECT="probe.txt"
CONTENT="abc-nodes-storage-probe-$$"
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

cleanup() {
  mc rb --force "$ALIAS/$BUCKET" >/dev/null 2>&1 || true
  mc alias remove "$ALIAS"        >/dev/null 2>&1 || true
  rm -f /tmp/minio-probe-$$
}
trap cleanup EXIT

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes MinIO storage CRUD test"
printf "  endpoint: %s\n" "$MINIO_URL"
echo "══════════════════════════════════════════════════════"
echo ""

# ─── Dependency check ─────────────────────────────────────────────────────────
echo "  ── Dependency check ─────────────────────────────────"
mc_bin=$(command -v mc 2>/dev/null || true)
if [ -z "$mc_bin" ]; then
  printf "  [SKIP] MinIO client 'mc' not found on host runtime\n"
  printf "         Skipping CRUD checks (install minio/mc to enable)\n"
  echo ""
  echo "══════════════════════════════════════════════════════"
  printf "  Results: %d passed,  %d failed\n" "$PASS" "$FAIL"
  echo "══════════════════════════════════════════════════════"
  exit 0
fi
if mc --help 2>&1 | grep -q "GNU Midnight Commander"; then
  printf "  [SKIP] 'mc' is not the MinIO client binary on host runtime\n"
  printf "         Skipping CRUD checks (install minio/mc to enable)\n"
  echo ""
  echo "══════════════════════════════════════════════════════"
  printf "  Results: %d passed,  %d failed\n" "$PASS" "$FAIL"
  echo "══════════════════════════════════════════════════════"
  exit 0
fi
ok "mc binary found at $mc_bin"

# ─── Credentials ──────────────────────────────────────────────────────────────
echo ""
echo "  ── Authentication ───────────────────────────────────"
if [ -z "$MINIO_SECRET_KEY" ]; then
  nok "MINIO_SECRET_KEY is empty — run: bash deployments/abc-nodes/scripts/store-test-secrets.sh"
  echo ""
  echo "══════════════════════════════════════════════════════"
  printf "  Results: %d passed,  %d failed\n" "$PASS" "$FAIL"
  echo "══════════════════════════════════════════════════════"
  exit 1
fi

mc alias set "$ALIAS" "$MINIO_URL" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY" \
  --api S3v4 >/dev/null 2>&1 \
  && ok  "mc alias set (authenticated)" \
  || { nok "mc alias set failed — check credentials"; exit 1; }

# Verify connection with a simple admin ping
mc admin info "$ALIAS" >/dev/null 2>&1 \
  && ok  "mc admin info (connection verified)" \
  || nok "mc admin info failed — MinIO may be unreachable"

# ─── Bucket existence ─────────────────────────────────────────────────────────
echo ""
echo "  ── Required bucket existence ────────────────────────"
for b in loki ntfy; do
  mc ls "$ALIAS/$b" >/dev/null 2>&1 \
    && ok  "bucket '$b' exists" \
    || nok "bucket '$b' missing — was it created by its service?"
done

# ─── Object round-trip ────────────────────────────────────────────────────────
echo ""
echo "  ── Object lifecycle (bucket: $BUCKET) ───────────────"

# Create test bucket
mc mb "$ALIAS/$BUCKET" >/dev/null 2>&1 \
  && ok  "mc mb — created bucket '$BUCKET'" \
  || nok "mc mb — failed to create bucket '$BUCKET'"

# Upload a test object
echo "$CONTENT" | mc pipe "$ALIAS/$BUCKET/$OBJECT" >/dev/null 2>&1 \
  && ok  "mc pipe — uploaded object '$OBJECT'" \
  || nok "mc pipe — failed to upload '$OBJECT'"

# Download and verify content
downloaded=$(mc cat "$ALIAS/$BUCKET/$OBJECT" 2>/dev/null)
if [ "$downloaded" = "$CONTENT" ]; then
  ok  "mc cat — content verified (round-trip match)"
else
  nok "mc cat — content mismatch: got '$downloaded'  want '$CONTENT'"
fi

# Stat the object (verify metadata)
mc stat "$ALIAS/$BUCKET/$OBJECT" >/dev/null 2>&1 \
  && ok  "mc stat — object metadata accessible" \
  || nok "mc stat — failed to stat '$OBJECT'"

# Delete object
mc rm "$ALIAS/$BUCKET/$OBJECT" >/dev/null 2>&1 \
  && ok  "mc rm — object deleted" \
  || nok "mc rm — failed to delete '$OBJECT'"

# Verify gone
mc stat "$ALIAS/$BUCKET/$OBJECT" >/dev/null 2>&1 \
  && nok "object still exists after mc rm" \
  || ok  "object confirmed absent after delete"

# Remove test bucket
mc rb "$ALIAS/$BUCKET" >/dev/null 2>&1 \
  && ok  "mc rb — bucket removed" \
  || nok "mc rb — failed to remove bucket '$BUCKET'"

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
