#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# Import Existing abc-nodes Services into Terraform State
# ═══════════════════════════════════════════════════════════════════════════
#
# This script imports all currently running abc-nodes services into Terraform
# state, allowing you to manage them without recreating them.
#
# Usage:
#   export NOMAD_TOKEN="your-token-here"
#   export NOMAD_ADDR="http://100.77.21.36:4646"
#   cd terraform
#   ./import-existing-services.sh

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check prerequisites
if ! command -v terraform &> /dev/null; then
    echo -e "${RED}ERROR: terraform not found. Please install Terraform first.${NC}"
    exit 1
fi

if [ -z "${NOMAD_TOKEN:-}" ]; then
    echo -e "${YELLOW}WARNING: NOMAD_TOKEN not set. Import may fail.${NC}"
fi

if [ -z "${NOMAD_ADDR:-}" ]; then
    echo -e "${YELLOW}WARNING: NOMAD_ADDR not set. Import may fail.${NC}"
fi

echo -e "${GREEN}Starting import of existing abc-nodes services...${NC}\n"

# Import function
import_service() {
    local resource_name=$1
    local job_id=$2
    
    echo -e "Importing ${YELLOW}${resource_name}${NC} (job: ${job_id})..."
    
    if terraform import "${resource_name}" "${job_id}" > /dev/null 2>&1; then
        echo -e "  ${GREEN}✓${NC} Successfully imported ${resource_name}"
        return 0
    else
        echo -e "  ${RED}✗${NC} Failed to import ${resource_name}"
        return 1
    fi
}

# Track success/failure
total=0
success=0
failed=0

# Core services (always deployed)
core_services=(
    "nomad_job.traefik:abc-nodes-traefik"
    "nomad_job.minio:abc-nodes-minio"
    "nomad_job.rustfs:abc-nodes-rustfs"
    "nomad_job.tusd:abc-nodes-tusd"
    "nomad_job.uppy:abc-nodes-uppy"
    "nomad_job.ntfy:abc-nodes-ntfy"
    "nomad_job.job_notifier:abc-nodes-job-notifier"
    "nomad_job.auth:abc-nodes-auth"
)

echo -e "\n${GREEN}=== Core Services ===${NC}"
for service in "${core_services[@]}"; do
    IFS=':' read -r resource job_id <<< "$service"
    total=$((total + 1))
    if import_service "$resource" "$job_id"; then
        success=$((success + 1))
    else
        failed=$((failed + 1))
    fi
done

# Observability services (conditional)
observability_services=(
    "nomad_job.prometheus[0]:abc-nodes-prometheus"
    "nomad_job.loki[0]:abc-nodes-loki"
    "nomad_job.grafana[0]:abc-nodes-grafana"
    "nomad_job.alloy[0]:abc-nodes-alloy"
)

echo -e "\n${GREEN}=== Observability Services ===${NC}"
echo -e "${YELLOW}Note: These will only import if deploy_observability_stack = true${NC}"
for service in "${observability_services[@]}"; do
    IFS=':' read -r resource job_id <<< "$service"
    total=$((total + 1))
    if import_service "$resource" "$job_id"; then
        success=$((success + 1))
    else
        failed=$((failed + 1))
    fi
done

# System services
system_services=(
    "nomad_job.boundary_worker[0]:abc-nodes-boundary-worker"
)

echo -e "\n${GREEN}=== System Services ===${NC}"
echo -e "${YELLOW}Note: These will only import if deploy_boundary_worker = true${NC}"
for service in "${system_services[@]}"; do
    IFS=':' read -r resource job_id <<< "$service"
    total=$((total + 1))
    if import_service "$resource" "$job_id"; then
        success=$((success + 1))
    else
        failed=$((failed + 1))
    fi
done

# Optional services
optional_services=(
    "nomad_job.docker_registry[0]:abc-nodes-docker-registry"
)

echo -e "\n${GREEN}=== Optional Services ===${NC}"
echo -e "${YELLOW}Note: These will only import if deploy_optional_services = true${NC}"
for service in "${optional_services[@]}"; do
    IFS=':' read -r resource job_id <<< "$service"
    total=$((total + 1))
    if import_service "$resource" "$job_id"; then
        success=$((success + 1))
    else
        failed=$((failed + 1))
    fi
done

# Summary
echo -e "\n${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}Import Summary${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "Total attempted: ${total}"
echo -e "${GREEN}Successful: ${success}${NC}"
if [ $failed -gt 0 ]; then
    echo -e "${RED}Failed: ${failed}${NC}"
else
    echo -e "Failed: ${failed}"
fi

# Next steps
echo -e "\n${GREEN}Next Steps:${NC}"
echo -e "1. Run ${YELLOW}terraform plan${NC} to verify imported state"
echo -e "2. Fix any discrepancies between .nomad.hcl files and running jobs"
echo -e "3. Run ${YELLOW}terraform apply${NC} when ready"
echo -e "\nFor more information, see README.md"

exit 0
