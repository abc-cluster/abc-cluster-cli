#!/usr/bin/env bash
# flush-dns-cache.sh
#
# Flush the local DNS cache and verify that Tailscale split-DNS is correctly
# resolving *.aither to aither's Tailscale IP (100.70.185.46).
#
# Run this on a TAILNET DEVICE (laptop / workstation) when *.aither links
# stop resolving after the split-DNS nameserver was already configured.
#
# Tailscale admin console setup (one-time):
#   DNS → Add nameserver → 100.70.185.46
#   Restrict to domain: aither   ← no leading dot
#
# Usage:
#   bash deployments/abc-nodes/dns/flush-dns-cache.sh

set -euo pipefail

AITHER_TS_IP="100.70.185.46"
DOMAIN="aither"
TEST_HOST="nomad.${DOMAIN}"

# ── 1. Flush OS DNS cache ────────────────────────────────────────────────────

OS="$(uname -s)"

echo "==> Flushing DNS cache on ${OS}..."

if [[ "${OS}" == "Darwin" ]]; then
  sudo dscacheutil -flushcache 2>/dev/null && \
  sudo killall -HUP mDNSResponder 2>/dev/null && \
  echo "    macOS DNS cache flushed" || \
  echo "    WARNING: flush failed (needs sudo) — run manually:
        sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder"

elif [[ "${OS}" == "Linux" ]]; then
  if command -v resolvectl &>/dev/null; then
    sudo resolvectl flush-caches 2>/dev/null && \
    echo "    systemd-resolved cache flushed" || \
    echo "    WARNING: flush failed (needs sudo) — run manually:
        sudo resolvectl flush-caches"
  elif command -v systemd-resolve &>/dev/null; then
    sudo systemd-resolve --flush-caches 2>/dev/null && \
    echo "    systemd-resolved cache flushed" || \
    echo "    WARNING: flush failed — run manually:
        sudo systemd-resolve --flush-caches"
  else
    echo "    WARNING: no known cache flush tool found (nscd? dnsmasq?)"
  fi

else
  echo "    WARNING: unsupported OS '${OS}' — flush manually"
fi

# ── 2. Verify nameserver is reachable ───────────────────────────────────────

echo ""
echo "==> Checking nameserver reachability: ${AITHER_TS_IP}:53"
if dig "@${AITHER_TS_IP}" "${TEST_HOST}" +short +time=3 +tries=1 2>/dev/null \
    | grep -q "${AITHER_TS_IP}"; then
  echo "    ✓ ${TEST_HOST} → ${AITHER_TS_IP}  (nameserver is reachable)"
else
  echo "    ✗ Could not resolve ${TEST_HOST} via ${AITHER_TS_IP}:53"
  echo "      Check that Tailscale is connected and aither is reachable:"
  echo "        ping ${AITHER_TS_IP}"
fi

# ── 3. Show which nameserver the OS is actually using for .aither ───────────

echo ""
echo "==> Split-DNS config on this device:"

if [[ "${OS}" == "Darwin" ]]; then
  scutil --dns 2>/dev/null | grep -A6 "${DOMAIN}" || \
    echo "    (no split-DNS entry found for '${DOMAIN}' — check Tailscale admin console)"

elif [[ "${OS}" == "Linux" ]]; then
  if command -v resolvectl &>/dev/null; then
    resolvectl query "${TEST_HOST}" 2>&1 || true
  fi
fi

# ── 4. End-to-end HTTP check ─────────────────────────────────────────────────

echo ""
echo "==> End-to-end HTTP check (requires split-DNS to be working):"
for svc in nomad grafana consul traefik; do
  url="http://${svc}.${DOMAIN}/"
  code=$(curl --noproxy '*' -sS -o /dev/null -w "%{http_code}" \
              --max-time 5 "${url}" 2>/dev/null || echo "ERR")
  if [[ "${code}" =~ ^(2|3) ]]; then
    echo "    ✓ ${url} → ${code}"
  else
    echo "    ✗ ${url} → ${code}"
  fi
done

echo ""
echo "Done. If links still fail, reconnect Tailscale and re-run this script."
