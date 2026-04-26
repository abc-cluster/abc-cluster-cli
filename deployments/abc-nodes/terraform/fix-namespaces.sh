#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# Fix Namespace Declarations in Nomad Job Files
# ═══════════════════════════════════════════════════════════════════════════
#
# Problem: The .nomad.hcl files declare various namespaces (abc-services,
# services, abc-applications) but ALL services are actually running in the
# 'default' namespace.
#
# This script updates all job files to use namespace = "default" to match
# the actual deployment.
#
# Usage:
#   cd terraform
#   ./fix-namespaces.sh
#
# NOTE: This modifies files in ../nomad/ - commit changes after running!

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

NOMAD_DIR="../nomad"

echo -e "${GREEN}Fixing namespace declarations in Nomad job files...${NC}\n"

# List of files to fix
job_files=(
    "traefik.nomad.hcl"
    "minio.nomad.hcl"
    "rustfs.nomad.hcl"
    "prometheus.nomad.hcl"
    "loki.nomad.hcl"
    "grafana.nomad.hcl"
    "alloy.nomad.hcl"
    "tusd.nomad.hcl"
    "uppy.nomad.hcl"
    "ntfy.nomad.hcl"
    "job-notifier.nomad.hcl"
    "abc-nodes-auth.nomad.hcl"
    "boundary-worker.nomad.hcl"
    "docker-registry.nomad.hcl"
)

fixed=0
skipped=0

for file in "${job_files[@]}"; do
    filepath="${NOMAD_DIR}/${file}"
    
    if [ ! -f "$filepath" ]; then
        echo -e "${YELLOW}⚠${NC} Skipping ${file} (not found)"
        skipped=$((skipped + 1))
        continue
    fi
    
    # Check current namespace
    current_ns=$(grep -m1 "^  namespace" "$filepath" 2>/dev/null | sed -E 's/.*"([^"]+)".*/\1/' || echo "NONE")
    
    if [ "$current_ns" = "default" ]; then
        echo -e "${GREEN}✓${NC} ${file} already set to 'default'"
        continue
    fi
    
    # Backup before modifying
    cp "$filepath" "${filepath}.bak"
    
    # Replace namespace declaration
    if sed -i '' 's/^  namespace   = ".*"/  namespace   = "default"/' "$filepath" 2>/dev/null || \
       sed -i '' 's/^  namespace = ".*"/  namespace = "default"/' "$filepath" 2>/dev/null; then
        echo -e "${GREEN}✓${NC} Fixed ${file} (was: ${current_ns} → now: default)"
        fixed=$((fixed + 1))
    else
        echo -e "${RED}✗${NC} Failed to fix ${file}"
        mv "${filepath}.bak" "$filepath"
    fi
done

echo -e "\n${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}Summary${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "Fixed: ${fixed}"
echo -e "Skipped: ${skipped}"

if [ $fixed -gt 0 ]; then
    echo -e "\n${YELLOW}IMPORTANT:${NC}"
    echo -e "1. Review changes: git diff ../nomad/"
    echo -e "2. Backup files created: *.bak"
    echo -e "3. Commit changes: git add ../nomad/ && git commit -m 'fix: correct namespace declarations to default'"
fi

exit 0
