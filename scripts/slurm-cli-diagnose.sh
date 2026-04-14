#!/bin/bash
#ABC --name=slurm-cli-diagnose
#ABC --driver=raw_exec
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=128M
set -euo pipefail

STATUS=0

section() {
  echo
  echo "========== $1 =========="
}

ok() {
  echo "[PASS] $1"
}

warn() {
  echo "[FAIL] $1"
  STATUS=1
}

run_check() {
  local label="$1"
  shift
  if "$@"; then
    ok "$label"
  else
    warn "$label"
  fi
}

section "Environment"
echo "whoami=$(whoami)"
echo "hostname=$(hostname)"
echo "date=$(date -Is)"
echo "PATH=${PATH}"

section "Slurm binaries"
run_check "sbatch available" command -v sbatch >/dev/null
run_check "scontrol available" command -v scontrol >/dev/null
run_check "sinfo available" command -v sinfo >/dev/null

section "Controller config"
SLURM_CONF="${SLURM_CONF:-/etc/slurm/slurm.conf}"
if [[ -f "${SLURM_CONF}" ]]; then
  echo "slurm_conf=${SLURM_CONF}"
  grep -E '^(SlurmctldHost|ControlMachine)=' "${SLURM_CONF}" || true
  SLURMCTLD_HOST="$(awk -F= '/^SlurmctldHost=/{print $2; exit}' "${SLURM_CONF}" | awk '{print $1}')"
  if [[ -z "${SLURMCTLD_HOST}" ]]; then
    SLURMCTLD_HOST="$(awk -F= '/^ControlMachine=/{print $2; exit}' "${SLURM_CONF}" | awk '{print $1}')"
  fi
else
  warn "slurm.conf not found at ${SLURM_CONF}"
  SLURMCTLD_HOST=""
fi

if [[ -n "${SLURMCTLD_HOST}" ]]; then
  echo "detected_slurmctld_host=${SLURMCTLD_HOST}"
  section "Controller DNS and network"
  if getent hosts "${SLURMCTLD_HOST}" >/tmp/slurmctld_hosts.out 2>/tmp/slurmctld_hosts.err; then
    ok "controller hostname resolves"
    cat /tmp/slurmctld_hosts.out
  else
    warn "controller hostname does not resolve"
    cat /tmp/slurmctld_hosts.err || true
  fi

  if command -v nc >/dev/null 2>&1; then
    if nc -z -w 5 "${SLURMCTLD_HOST}" 6817; then
      ok "tcp connect to slurmctld:6817"
    else
      warn "cannot connect to slurmctld:6817"
    fi
  else
    echo "[INFO] nc not available; skipping port probe"
  fi
else
  warn "controller hostname not detected from slurm.conf"
fi

section "Local controller service checks"
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet slurmctld; then
    ok "slurmctld service active"
  else
    warn "slurmctld service inactive"
    systemctl status slurmctld --no-pager -n 30 || true
  fi
else
  echo "[INFO] systemctl not available; skipping service status"
fi

if command -v ss >/dev/null 2>&1; then
  if ss -ltn | grep -q ':6817'; then
    ok "local listener present on :6817"
  else
    warn "no local listener on :6817"
    ss -ltn || true
  fi
else
  echo "[INFO] ss not available; skipping listener check"
fi

section "Slurm control plane checks"
if scontrol ping >/tmp/scontrol_ping.out 2>/tmp/scontrol_ping.err; then
  ok "scontrol ping"
  cat /tmp/scontrol_ping.out
else
  warn "scontrol ping"
  cat /tmp/scontrol_ping.err || true
fi

if sinfo -N -o "%N %T %C" >/tmp/sinfo.out 2>/tmp/sinfo.err; then
  ok "sinfo node query"
  cat /tmp/sinfo.out
else
  warn "sinfo node query"
  cat /tmp/sinfo.err || true
fi

section "Submit smoke test"
if sbatch --parsable --wrap "hostname; date" >/tmp/sbatch.out 2>/tmp/sbatch.err; then
  ok "sbatch submit test"
  echo "job_id=$(cat /tmp/sbatch.out)"
else
  warn "sbatch submit test"
  cat /tmp/sbatch.err || true
fi

echo
if [[ "${STATUS}" -eq 0 ]]; then
  echo "SLURM_DIAG_RESULT=PASS"
else
  echo "SLURM_DIAG_RESULT=FAIL"
fi

exit "${STATUS}"
