# Test: HashiCorp Vault — seal status and KV round-trip
#
# Tests:
#   /v1/sys/health       — Vault is initialized and unsealed
#   /v1/sys/mounts       — secret/ KV v2 mount is present (requires token)
#   KV round-trip        — write a value, read it back, verify, delete
#   /v1/sys/seal-status  — double-check sealed=false
#
# Requires a Vault token with KV read/write access.
# Token is read from nomad/jobs/abc-nodes-test-vault (key: vault_token) at runtime,
# with fallback to the -var vault_token=<token> HCL variable.
#
# Setup (once):
#   bash deployments/abc-nodes/scripts/store-test-secrets.sh
#
# Run:
#   abc admin services nomad cli -- job run -detach \
#     deployments/abc-nodes/experimental/nomad/tests/vault.nomad.hcl
#
# Or with an explicit token:
#   abc admin services nomad cli -- job run -detach \
#     -var vault_token=<root-or-rw-token> \
#     deployments/abc-nodes/experimental/nomad/tests/vault.nomad.hcl

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "node_ip" {
  type    = string
  default = "100.70.185.46"
}

variable "vault_token" {
  type        = string
  default     = ""
  description = "Vault token for KV tests. Override via -var or Nomad Variable nomad/jobs/abc-nodes-test-vault."
}

variable "kv_path" {
  type    = string
  default = "secret/data/abc-nodes-test-probe"
  description = "KV v2 path to use for the write/read/delete round-trip test."
}

job "abc-nodes-test-vault" {
  namespace   = "services"
  type        = "batch"
  priority    = 50
  datacenters = var.datacenters

  meta {
    abc_cluster_type = "abc-nodes"
    purpose          = "test"
    test_suite       = "vault"
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
        destination = "secrets/vault.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-test-vault" -}}
VAULT_TEST_TOKEN={{ .vault_token }}
{{- else -}}
VAULT_TEST_TOKEN=${var.vault_token}
{{- end }}
EOF
      }

      template {
        destination = "local/test.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
# abc-nodes Vault test
set -u

VAULT_ADDR="http://${var.node_ip}:8200"
KV_PATH="${var.kv_path}"
# Derive the metadata path from the data path for delete
KV_META_PATH=$(echo "$KV_PATH" | sed 's|/data/|/metadata/|')
PROBE_VALUE="abc-nodes-vault-probe-$$"
PASS=0
FAIL=0

ok()  { PASS=$((PASS+1)); printf "  [PASS] %s\n" "$*"; }
nok() { FAIL=$((FAIL+1)); printf "  [FAIL] %s\n" "$*"; }

vault_get()  { curl -sL --max-time 5 -H "X-Vault-Token: $VAULT_TEST_TOKEN" "$VAULT_ADDR/$1" 2>/dev/null; }
vault_code() { curl -s -o /dev/null -w "%%{http_code}" --max-time 5 -H "X-Vault-Token: $VAULT_TEST_TOKEN" "$VAULT_ADDR/$1" 2>/dev/null; }

echo ""
echo "══════════════════════════════════════════════════════"
echo "  abc-nodes Vault test"
printf "  addr: %s\n" "$VAULT_ADDR"
echo "══════════════════════════════════════════════════════"

# ─── Seal status (no token required) ─────────────────────────────────────────
echo ""
echo "  ── Seal status ──────────────────────────────────────"

health_body=$(curl -sL --max-time 5 "$VAULT_ADDR/v1/sys/health" 2>/dev/null)
health_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 "$VAULT_ADDR/v1/sys/health" 2>/dev/null)

initialized=$(echo "$health_body" | jq -r 'if has("initialized") then (.initialized|tostring) else "unknown" end' 2>/dev/null)
sealed=$(echo "$health_body" | jq -r 'if has("sealed") then (.sealed|tostring) else "unknown" end' 2>/dev/null)
version=$(echo "$health_body" | jq -r '.version // "unknown"' 2>/dev/null)

case "$health_code" in
  200)
    ok "Vault /v1/sys/health  →  HTTP 200 (initialized, unsealed)  version=$version"
    ;;
  429)
    ok "Vault /v1/sys/health  →  HTTP 429 (standby)  version=$version"
    ;;
  503)
    nok "Vault /v1/sys/health  →  HTTP 503 SEALED  (run: bash deployments/abc-nodes/experimental/scripts/init-vault.sh)"
    ;;
  501)
    nok "Vault /v1/sys/health  →  HTTP 501 NOT INITIALIZED  (run: bash deployments/abc-nodes/experimental/scripts/init-vault.sh)"
    ;;
  *)
    nok "Vault /v1/sys/health  →  HTTP $health_code  body=$health_body"
    ;;
esac

# Explicit field checks
[ "$initialized" = "true" ] \
  && ok  "Vault initialized=true" \
  || nok "Vault initialized=$initialized (expected true)"
[ "$sealed" = "false" ] \
  && ok  "Vault sealed=false" \
  || nok "Vault sealed=$sealed (expected false — unseal with init-vault.sh)"

# ─── Token-gated tests ────────────────────────────────────────────────────────
echo ""
echo "  ── Token-gated tests ────────────────────────────────"

if [ -z "$VAULT_TEST_TOKEN" ] || [ "$VAULT_TEST_TOKEN" = "PLACEHOLDER_NOT_SET" ]; then
  printf "  [SKIP] VAULT_TEST_TOKEN not set — skipping KV and mount tests\n"
  printf "         Set VAULT_TEST_TOKEN=<token> and re-run store-test-secrets.sh\n"
else
  # Verify token is valid
  token_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
    -H "X-Vault-Token: $VAULT_TEST_TOKEN" \
    "$VAULT_ADDR/v1/auth/token/lookup-self" 2>/dev/null)
  [ "$token_code" = "200" ] \
    && ok  "Vault token valid  →  /auth/token/lookup-self HTTP 200" \
    || nok "Vault token invalid  →  HTTP $token_code (check VAULT_TEST_TOKEN)"

  # Check secret/ mount is present
  mounts_body=$(vault_get "v1/sys/mounts")
  echo "$mounts_body" | jq -e '."secret/"' >/dev/null 2>&1 \
    && ok  "Vault mount secret/  →  KV v2 present" \
    || nok "Vault mount secret/  →  not found (run: vault secrets enable -path=secret kv-v2)"

  # ─── KV round-trip ──────────────────────────────────────────────────────────
  echo ""
  echo "  ── KV v2 round-trip ($KV_PATH) ──────────────"

  # Write
  write_payload='{"data":{"probe":"'"$PROBE_VALUE"'","timestamp":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'"}}'
  write_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
    -X POST -H "Content-Type: application/json" \
    -H "X-Vault-Token: $VAULT_TEST_TOKEN" \
    -d "$write_payload" \
    "$VAULT_ADDR/v1/$KV_PATH" 2>/dev/null)
  [ "$write_code" = "200" ] \
    && ok  "KV write  →  HTTP $write_code" \
    || nok "KV write  →  HTTP $write_code (expected 200 — check token permissions)"

  # Read back
  read_body=$(vault_get "v1/$KV_PATH")
  read_value=$(echo "$read_body" | jq -r '.data.data.probe // empty' 2>/dev/null)
  [ "$read_value" = "$PROBE_VALUE" ] \
    && ok  "KV read  →  value matches (round-trip verified)" \
    || nok "KV read  →  got '$read_value'  want '$PROBE_VALUE'"

  # Verify version metadata
  version_num=$(echo "$read_body" | jq -r '.data.metadata.version // 0' 2>/dev/null)
  [ "$version_num" -ge 1 ] 2>/dev/null \
    && ok  "KV metadata  →  version=$version_num" \
    || nok "KV metadata  →  unexpected version field: $version_num"

  # Delete (cleanup)
  del_code=$(curl -s -o /dev/null -w "%%{http_code}" --max-time 5 \
    -X DELETE \
    -H "X-Vault-Token: $VAULT_TEST_TOKEN" \
    "$VAULT_ADDR/v1/$KV_META_PATH" 2>/dev/null)
  [ "$del_code" = "204" ] \
    && ok  "KV delete (cleanup)  →  HTTP $del_code" \
    || nok "KV delete (cleanup)  →  HTTP $del_code (expected 204)"
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
