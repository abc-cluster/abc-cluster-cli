#!/usr/bin/env bash
set -euo pipefail

# Keep Grafana dashboard variable definitions in sync with ACL sources of truth:
# - namespaces (from acl/namespaces/*.hcl)
# - research users (from acl/setup-minio-namespace-buckets.sh NS_USERS map)
# - buckets (same as namespaces)
#
# Usage:
#   bash deployments/abc-nodes/nomad/sync-grafana-definitions.sh
#
# To sync and redeploy Grafana in one step:
#   bash deployments/abc-nodes/nomad/scripts/redeploy-grafana-dashboards.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
ACL_DIR="${ROOT_DIR}/acl"
NAMESPACES_DIR="${ACL_DIR}/namespaces"
MINIO_SETUP="${ACL_DIR}/setup-minio-namespace-buckets.sh"
USAGE_DASH="${SCRIPT_DIR}/grafana-dashboard-usage-overview.json"
BUCKET_DASH="${SCRIPT_DIR}/grafana-dashboard-bucket-usage.json"

python3 - "${NAMESPACES_DIR}" "${MINIO_SETUP}" "${USAGE_DASH}" "${BUCKET_DASH}" <<'PY'
import json
import re
import sys
from pathlib import Path

namespaces_dir = Path(sys.argv[1])
minio_setup = Path(sys.argv[2])
usage_dash = Path(sys.argv[3])
bucket_dash = Path(sys.argv[4])

if not namespaces_dir.exists():
    raise SystemExit(f"missing namespaces dir: {namespaces_dir}")
if not minio_setup.exists():
    raise SystemExit(f"missing minio setup script: {minio_setup}")
if not usage_dash.exists():
    raise SystemExit(f"missing usage dashboard json: {usage_dash}")
if not bucket_dash.exists():
    raise SystemExit(f"missing bucket dashboard json: {bucket_dash}")

# 1) Namespace source of truth from acl/namespaces/*.hcl
ignored = {"abc-services", "abc-applications"}
namespaces = sorted(
    p.stem for p in namespaces_dir.glob("*.hcl")
    if p.stem not in ignored
)

# 2) User source of truth from NS_USERS lines in setup-minio-namespace-buckets.sh
ns_users = {}
pattern = re.compile(r'^NS_USERS\["([^"]+)"\]="([^"]*)"$')
for line in minio_setup.read_text().splitlines():
    m = pattern.match(line.strip())
    if not m:
        continue
    ns = m.group(1)
    users_csv = m.group(2).strip()
    users = [u.strip() for u in users_csv.split(",") if u.strip()]
    ns_users[ns] = users

research_users = []
for ns in namespaces:
    for u in ns_users.get(ns, []):
        research_users.append(f"{ns}_{u}")
research_users = sorted(set(research_users))

if not namespaces:
    raise SystemExit("no research namespaces discovered")

def make_custom_var(name: str, label: str, values: list[str], include_all: bool = True):
    query = ",".join(values)
    current = {"selected": True, "text": "All", "value": "$__all"} if include_all else {"selected": True, "text": values[0], "value": values[0]}
    return {
        "name": name,
        "label": label,
        "type": "custom",
        "query": query,
        "options": [],
        "current": current,
        "refresh": 2,
        "sort": 1,
        "multi": include_all,
        "includeAll": include_all,
        "allValue": ".*" if include_all else None,
    }

def upsert_variable(dashboard: dict, new_var: dict):
    vars_list = dashboard.setdefault("templating", {}).setdefault("list", [])
    for i, v in enumerate(vars_list):
        if v.get("name") == new_var["name"]:
            # Keep existing position but replace definition fully.
            vars_list[i] = new_var
            return
    vars_list.append(new_var)

# 3) Update usage overview namespace + research_user variables
usage = json.loads(usage_dash.read_text())
upsert_variable(
    usage,
    make_custom_var(
        "namespace",
        "Group / Namespace",
        namespaces,
        include_all=True,
    ),
)
if research_users:
    upsert_variable(
        usage,
        make_custom_var(
            "research_user",
            "Research user (namespace_user)",
            research_users,
            include_all=True,
        ),
    )
usage["version"] = int(usage.get("version", 1)) + 1
usage_dash.write_text(json.dumps(usage, indent=2) + "\n")

# 4) Update bucket dashboard bucket variable from known namespace buckets
bucket = json.loads(bucket_dash.read_text())
upsert_variable(
    bucket,
    make_custom_var(
        "bucket",
        "Bucket",
        namespaces,
        include_all=True,
    ),
)
bucket["version"] = int(bucket.get("version", 1)) + 1
bucket_dash.write_text(json.dumps(bucket, indent=2) + "\n")

print(f"namespaces={len(namespaces)}")
print(f"research_users={len(research_users)}")
print(f"updated={usage_dash.name},{bucket_dash.name}")
PY
