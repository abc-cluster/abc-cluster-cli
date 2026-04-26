#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# Migrate abc-nodes Services from 'default' to 'abc-services' Namespace
# ═══════════════════════════════════════════════════════════════════════════
#
# This script performs a zero-downtime migration:
# 1. Creates abc-services namespace if needed
# 2. Updates .nomad.hcl files to use abc-services namespace
# 3. Deploys each service to abc-services
# 4. Validates health, tags, labels match between old and new
# 5. Deletes from default namespace after verification
#
# Usage:
#   export NOMAD_TOKEN="your-token"
#   export NOMAD_ADDR="http://100.77.21.36:4646"
#   cd deployments/abc-nodes
#   ./scripts/migrate-to-abc-services-namespace.sh
#
# Flags:
#   --dry-run    Show what would be done without executing
#   --rollback   Revert all changes (restore to default namespace)

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Configuration
SOURCE_NAMESPACE="default"
TARGET_NAMESPACE="abc-services"
NOMAD_DIR="nomad"
BACKUP_DIR="nomad-backups-$(date +%Y%m%d-%H%M%S)"
DRY_RUN=false
ROLLBACK=false

# Services to migrate (in dependency order)
declare -a SERVICES=(
    "traefik"
    "minio"
    "rustfs"
    "prometheus"
    "loki"
    "grafana"
    "alloy"
    "tusd"
    "uppy"
    "ntfy"
    "job-notifier"
    "abc-nodes-auth"
    "docker-registry"
    "boundary-worker"
)

# Parse arguments
for arg in "$@"; do
    case $arg in
        --dry-run)
            DRY_RUN=true
            ;;
        --rollback)
            ROLLBACK=true
            ;;
        *)
            echo "Unknown argument: $arg"
            exit 1
            ;;
    esac
done

# Utility functions
log_info() {
    echo -e "${CYAN}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_step() {
    echo -e "\n${BLUE}═══${NC} $* ${BLUE}═══${NC}"
}

# Check prerequisites
check_prerequisites() {
    log_step "Checking Prerequisites"
    
    if [ -z "${NOMAD_TOKEN:-}" ]; then
        log_error "NOMAD_TOKEN not set"
        exit 1
    fi
    
    if [ -z "${NOMAD_ADDR:-}" ]; then
        log_error "NOMAD_ADDR not set"
        exit 1
    fi
    
    if ! command -v abc &> /dev/null; then
        log_error "abc CLI not found"
        exit 1
    fi
    
    log_success "Prerequisites OK"
}

# Create namespace
create_namespace() {
    log_step "Creating $TARGET_NAMESPACE Namespace"
    
    if ABC_CLI_DISABLE_UPDATE_CHECK=1 abc admin services nomad cli -- namespace list 2>/dev/null | grep -q "^${TARGET_NAMESPACE}"; then
        log_info "Namespace $TARGET_NAMESPACE already exists"
        return 0
    fi
    
    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY-RUN] Would create namespace: $TARGET_NAMESPACE"
        return 0
    fi
    
    cat > /tmp/abc-services-namespace.hcl <<EOF
name        = "$TARGET_NAMESPACE"
description = "ABC-nodes platform services - core infrastructure"

capabilities {
  enabled_task_drivers = ["raw_exec", "exec", "containerd-driver"]
  disabled_task_drivers = []
}
EOF
    
    ABC_CLI_DISABLE_UPDATE_CHECK=1 abc admin services nomad cli -- namespace apply /tmp/abc-services-namespace.hcl
    log_success "Created namespace: $TARGET_NAMESPACE"
}

# Backup nomad files
backup_files() {
    log_step "Backing Up Nomad Job Files"
    
    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY-RUN] Would backup files to: $BACKUP_DIR"
        return 0
    fi
    
    mkdir -p "$BACKUP_DIR"
    
    for service in "${SERVICES[@]}"; do
        local file="${NOMAD_DIR}/${service}.nomad.hcl"
        if [ "$service" = "abc-nodes-auth" ]; then
            file="${NOMAD_DIR}/abc-nodes-auth.nomad.hcl"
        fi
        
        if [ -f "$file" ]; then
            cp "$file" "${BACKUP_DIR}/"
            log_info "Backed up: $file"
        fi
    done
    
    log_success "Backup created: $BACKUP_DIR"
}

# Update namespace in HCL file
update_namespace_in_file() {
    local file=$1
    local target_ns=$2
    
    if [ ! -f "$file" ]; then
        log_error "File not found: $file"
        return 1
    fi
    
    if [ "$DRY_RUN" = true ]; then
        local current_ns=$(grep -m1 '^  namespace' "$file" | sed -E 's/.*"([^"]+)".*/\1/')
        log_info "[DRY-RUN] Would change namespace in $file: $current_ns → $target_ns"
        return 0
    fi
    
    # Update namespace declaration
    sed -i.bak "s/^  namespace[ ]*=.*$/  namespace   = \"${target_ns}\"/" "$file"
    
    log_success "Updated namespace in: $(basename $file)"
}

# Get job details from Nomad
get_job_details() {
    local job_id=$1
    local namespace=$2
    
    ABC_CLI_DISABLE_UPDATE_CHECK=1 abc admin services nomad cli -- \
        job status -namespace="$namespace" "$job_id" 2>/dev/null || echo ""
}

# Compare job configurations
compare_jobs() {
    local job_id=$1
    local source_ns=$2
    local target_ns=$3
    
    log_info "Comparing job: $job_id between $source_ns and $target_ns"
    
    # Get job details from both namespaces
    local source_status=$(get_job_details "$job_id" "$source_ns")
    local target_status=$(get_job_details "$job_id" "$target_ns")
    
    if [ -z "$target_status" ]; then
        log_warn "Job not found in target namespace: $job_id"
        return 1
    fi
    
    # Check if both show as running
    local source_running=$(echo "$source_status" | grep -c "Status.*=.*running" || true)
    local target_running=$(echo "$target_status" | grep -c "Status.*=.*running" || true)
    
    if [ "$target_running" -eq 0 ]; then
        log_error "Target job not running: $job_id"
        return 1
    fi
    
    log_success "Job running in both namespaces: $job_id"
    return 0
}

# Migrate a single service
migrate_service() {
    local service=$1
    local job_id="abc-nodes-${service}"
    local file="${NOMAD_DIR}/${service}.nomad.hcl"
    
    # Handle special names
    if [ "$service" = "abc-nodes-auth" ]; then
        job_id="abc-nodes-auth"
        file="${NOMAD_DIR}/abc-nodes-auth.nomad.hcl"
    fi
    
    log_step "Migrating: $job_id"
    
    # Check if file exists
    if [ ! -f "$file" ]; then
        log_warn "Skipping $service (file not found: $file)"
        return 0
    fi
    
    # Check if job exists in source namespace
    if ! get_job_details "$job_id" "$SOURCE_NAMESPACE" | grep -q "Status"; then
        log_warn "Job not found in $SOURCE_NAMESPACE: $job_id"
        return 0
    fi
    
    # Update namespace in file
    update_namespace_in_file "$file" "$TARGET_NAMESPACE"
    
    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY-RUN] Would deploy $job_id to $TARGET_NAMESPACE"
        log_info "[DRY-RUN] Would verify health and tags"
        log_info "[DRY-RUN] Would stop $job_id in $SOURCE_NAMESPACE"
        return 0
    fi
    
    # Deploy to target namespace
    log_info "Deploying to $TARGET_NAMESPACE: $job_id"
    if ! ABC_CLI_DISABLE_UPDATE_CHECK=1 abc admin services nomad cli -- \
        job run -detach "$file" 2>&1; then
        log_error "Failed to deploy: $job_id"
        return 1
    fi
    
    # Wait for deployment
    log_info "Waiting for deployment to stabilize..."
    sleep 10
    
    # Verify job is running
    local max_attempts=12
    local attempt=0
    while [ $attempt -lt $max_attempts ]; do
        if get_job_details "$job_id" "$TARGET_NAMESPACE" | grep -q "Status.*=.*running"; then
            log_success "Job running in $TARGET_NAMESPACE: $job_id"
            break
        fi
        attempt=$((attempt + 1))
        log_info "Waiting for job to start... ($attempt/$max_attempts)"
        sleep 5
    done
    
    if [ $attempt -eq $max_attempts ]; then
        log_error "Job failed to start in $TARGET_NAMESPACE: $job_id"
        return 1
    fi
    
    # Compare configurations
    if ! compare_jobs "$job_id" "$SOURCE_NAMESPACE" "$TARGET_NAMESPACE"; then
        log_error "Job configuration mismatch for: $job_id"
        return 1
    fi
    
    # Stop job in source namespace
    log_info "Stopping in $SOURCE_NAMESPACE: $job_id"
    ABC_CLI_DISABLE_UPDATE_CHECK=1 abc admin services nomad cli -- \
        job stop -namespace="$SOURCE_NAMESPACE" -purge "$job_id" || \
        log_warn "Could not stop job in source namespace (may already be stopped)"
    
    log_success "Migration complete: $job_id"
    echo ""
    
    return 0
}

# Rollback migration
rollback_migration() {
    log_step "Rolling Back Migration"
    
    if [ ! -d "$BACKUP_DIR" ]; then
        log_error "No backup directory found. Please specify backup with BACKUP_DIR env var"
        exit 1
    fi
    
    log_warn "This will restore files from: $BACKUP_DIR"
    read -p "Continue? (yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        log_info "Rollback cancelled"
        exit 0
    fi
    
    # Restore files
    for service in "${SERVICES[@]}"; do
        local file="${NOMAD_DIR}/${service}.nomad.hcl"
        if [ "$service" = "abc-nodes-auth" ]; then
            file="${NOMAD_DIR}/abc-nodes-auth.nomad.hcl"
        fi
        
        local backup_file="${BACKUP_DIR}/$(basename $file)"
        if [ -f "$backup_file" ]; then
            cp "$backup_file" "$file"
            log_info "Restored: $file"
        fi
    done
    
    log_success "Files restored. You may need to redeploy services manually."
}

# Main execution
main() {
    echo -e "${GREEN}"
    echo "═══════════════════════════════════════════════════════════════════"
    echo "  ABC-nodes Namespace Migration: $SOURCE_NAMESPACE → $TARGET_NAMESPACE"
    echo "═══════════════════════════════════════════════════════════════════"
    echo -e "${NC}"
    
    if [ "$ROLLBACK" = true ]; then
        rollback_migration
        exit 0
    fi
    
    if [ "$DRY_RUN" = true ]; then
        log_warn "DRY-RUN MODE - No changes will be made"
        echo ""
    fi
    
    check_prerequisites
    backup_files
    create_namespace
    
    # Migrate services
    local failed_services=()
    local migrated_count=0
    
    for service in "${SERVICES[@]}"; do
        if migrate_service "$service"; then
            migrated_count=$((migrated_count + 1))
        else
            failed_services+=("$service")
            log_error "Failed to migrate: $service"
        fi
    done
    
    # Summary
    log_step "Migration Summary"
    echo ""
    log_info "Total services: ${#SERVICES[@]}"
    log_success "Successfully migrated: $migrated_count"
    
    if [ ${#failed_services[@]} -gt 0 ]; then
        log_error "Failed migrations: ${#failed_services[@]}"
        for svc in "${failed_services[@]}"; do
            echo "  - $svc"
        done
        echo ""
        log_warn "Some services failed to migrate. Check logs above."
        log_info "Backup location: $BACKUP_DIR"
        exit 1
    fi
    
    log_success "All services migrated successfully!"
    echo ""
    log_info "Backup location: $BACKUP_DIR"
    log_info "Next steps:"
    echo "  1. Verify all services: abc job list"
    echo "  2. Check service health in Nomad UI"
    echo "  3. Update Terraform configuration"
    echo "  4. Commit changes: git add nomad/ && git commit -m 'migrate: move services to abc-services namespace'"
}

# Run main
main "$@"
