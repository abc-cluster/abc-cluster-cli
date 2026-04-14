#!/bin/bash
#ABC --name=slurm-ctrl-repair
#ABC --driver=raw_exec
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=256M
set -euo pipefail

echo "=== slurm controller repair start ==="
echo "host=$(hostname)"
echo "date=$(date -Is)"

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl is required on target node" >&2
  exit 1
fi

echo "--- current service state ---"
systemctl is-active munge || true
systemctl is-active slurmdbd || true
systemctl is-active slurmctld || true

echo "--- restart base services ---"
sudo systemctl restart munge
sudo systemctl restart slurmdbd

echo "--- wait for slurmdbd ---"
for i in {1..20}; do
  if systemctl is-active --quiet slurmdbd; then
    echo "slurmdbd is active"
    break
  fi
  sleep 2
done
systemctl is-active --quiet slurmdbd

echo "--- ensure accounting objects ---"
sudo sacctmgr -i add cluster gcp-test || true
sudo sacctmgr -i add account testaccount cluster=gcp-test description="Test account" || true
sudo sacctmgr -i add user slurm account=testaccount cluster=gcp-test || true

echo "--- restart controller ---"
sudo systemctl restart slurmctld
sleep 3

echo "--- post-restart checks ---"
systemctl is-active slurmctld
sudo ss -ltn | rg ":6817" || true
scontrol ping || true

echo "=== slurm controller repair done ==="
