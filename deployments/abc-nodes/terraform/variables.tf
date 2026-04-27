# ═══════════════════════════════════════════════════════════════════════════
# Nomad Provider
# ═══════════════════════════════════════════════════════════════════════════

variable "nomad_address" {
  description = "Nomad API address"
  type        = string
  default     = "http://100.77.21.36:4646"
}

variable "nomad_token" {
  description = "Nomad ACL token (prefer setting via NOMAD_TOKEN env var)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "nomad_region" {
  description = "Nomad region"
  type        = string
  default     = "global"
}

variable "nomad_namespace" {
  description = "Primary Nomad namespace for enhanced-tier platform services"
  type        = string
  default     = "abc-services"
}

# ═══════════════════════════════════════════════════════════════════════════
# Cluster Identity
# ═══════════════════════════════════════════════════════════════════════════

variable "datacenters" {
  description = "Nomad datacenters eligible for placement"
  type        = list(string)
  default     = ["dc1", "default"]
}

variable "cluster_public_host" {
  description = "Public hostname for cluster services"
  type        = string
  default     = "aither.mb.sun.ac.za"
}

variable "cluster_tailscale_ip" {
  description = "Tailscale IP of the main cluster node (aither)"
  type        = string
  default     = "100.70.185.46"
}

# ═══════════════════════════════════════════════════════════════════════════
# Enhanced Tier — Service Enable Flags
#
# Basic tier (minio, auth, tusd, uppy) is owned by the abc CLI and is NOT
# managed here.  All resources below default to enabled (true) except
# docker_registry which is explicitly optional.
# ═══════════════════════════════════════════════════════════════════════════

variable "enable_traefik" {
  description = "Deploy Traefik reverse-proxy / ingress"
  type        = bool
  default     = true
}

variable "enable_rustfs" {
  description = "Deploy RustFS S3-compatible object storage"
  type        = bool
  default     = true
}

variable "enable_prometheus" {
  description = "Deploy Prometheus metrics server"
  type        = bool
  default     = true
}

variable "enable_loki" {
  description = "Deploy Loki log aggregation"
  type        = bool
  default     = true
}

variable "enable_grafana" {
  description = "Deploy Grafana dashboards"
  type        = bool
  default     = true
}

variable "enable_alloy" {
  description = "Deploy Grafana Alloy (metrics + log collection agent)"
  type        = bool
  default     = true
}

variable "enable_ntfy" {
  description = "Deploy ntfy push-notification broker"
  type        = bool
  default     = true
}

variable "enable_job_notifier" {
  description = "Deploy job-notifier (Nomad event → ntfy bridge)"
  type        = bool
  default     = true
}

variable "enable_boundary_worker" {
  description = "Deploy Boundary worker system job across all nodes"
  type        = bool
  default     = true
}

variable "enable_docker_registry" {
  description = "Deploy private Docker registry (optional)"
  type        = bool
  default     = false
}

# ─── Deprecated compat shims ───────────────────────────────────────────────
# These three variables existed before per-service flags were introduced.
# They are preserved so existing tfvars / config.yaml entries keep working.
# When both a shim and a per-service flag are set, the per-service flag wins.
# Remove these after all contexts have been migrated.

variable "deploy_observability_stack" {
  description = "DEPRECATED — use enable_prometheus/loki/grafana/alloy instead"
  type        = bool
  default     = true
}

variable "deploy_boundary_worker" {
  description = "DEPRECATED — use enable_boundary_worker instead"
  type        = bool
  default     = true
}

variable "deploy_optional_services" {
  description = "DEPRECATED — use enable_docker_registry instead"
  type        = bool
  default     = false
}

# ═══════════════════════════════════════════════════════════════════════════
# Experimental Tier — Service Enable Flags
#
# All experimental services default to false (opt-in only).
# They run in the abc-experimental namespace.
# ═══════════════════════════════════════════════════════════════════════════

variable "enable_postgres" {
  description = "Deploy PostgreSQL (abc-experimental) — shared dep for Wave + Supabase"
  type        = bool
  default     = false
}

variable "enable_redis" {
  description = "Deploy Redis (abc-experimental) — Wave rate-limit / cache dep"
  type        = bool
  default     = false
}

variable "enable_wave" {
  description = "Deploy Wave container-build orchestrator (abc-experimental; needs postgres + redis)"
  type        = bool
  default     = false
}

variable "enable_supabase" {
  description = "Deploy Supabase BaaS (abc-experimental; needs postgres)"
  type        = bool
  default     = false
}

variable "enable_restic_server" {
  description = "Deploy Restic REST server for cluster backups (abc-experimental)"
  type        = bool
  default     = false
}

variable "enable_caddy" {
  description = "Deploy Caddy reverse-proxy (abc-experimental; integrates with Consul, Traefik, Tailscale)"
  type        = bool
  default     = false
}

variable "enable_xtdb" {
  description = "Deploy XTDB v2 bitemporal database (abc-experimental)"
  type        = bool
  default     = false
}

# ═══════════════════════════════════════════════════════════════════════════
# Service Images — Enhanced Tier
# ═══════════════════════════════════════════════════════════════════════════

variable "traefik_version" {
  description = "Traefik version"
  type        = string
  default     = "3.3.5"
}

variable "prometheus_image" {
  description = "Prometheus container image"
  type        = string
  default     = "prom/prometheus:v2.54.1"
}

variable "loki_image" {
  description = "Loki container image"
  type        = string
  default     = "grafana/loki:3.3.2"
}

variable "grafana_image" {
  description = "Grafana container image"
  type        = string
  default     = "grafana/grafana:11.4.0"
}

variable "alloy_version" {
  description = "Grafana Alloy version"
  type        = string
  default     = "1.15.1"
}

variable "ntfy_image" {
  description = "ntfy container image"
  type        = string
  default     = "binwiederhier/ntfy:v2.11.0"
}

variable "rustfs_image" {
  description = "RustFS container image"
  type        = string
  default     = "rustfs/rustfs:latest"
}

variable "docker_registry_image" {
  description = "Docker Registry container image"
  type        = string
  default     = "registry:2"
}

variable "boundary_version" {
  description = "Boundary version"
  type        = string
  default     = "0.18.2"
}

# ═══════════════════════════════════════════════════════════════════════════
# Service Images — Experimental Tier
# ═══════════════════════════════════════════════════════════════════════════

variable "postgres_image" {
  description = "PostgreSQL container image (abc-experimental)"
  type        = string
  default     = "postgres:15-alpine"
}

variable "redis_image" {
  description = "Redis container image (abc-experimental)"
  type        = string
  default     = "redis:7-alpine"
}

variable "supabase_image" {
  description = "Supabase Studio container image (abc-experimental)"
  type        = string
  default     = "supabase/studio:latest"
}

variable "restic_server_image" {
  description = "Restic REST server container image (abc-experimental)"
  type        = string
  default     = "restic/rest-server:latest"
}

# Wave image is TBD — left as empty string; the nomad HCL stub documents this.
variable "wave_image" {
  description = "Wave container image (abc-experimental; TBD from Wave release channel)"
  type        = string
  default     = ""
}

variable "caddy_image" {
  description = "Caddy container image (abc-experimental)"
  type        = string
  default     = "caddy:2-alpine"
}

# Pin to a specific release tag in production; 'latest' tracks the most recent
# 2.x release from ghcr.io/xtdb/xtdb.
variable "xtdb_image" {
  description = "XTDB v2 container image (abc-experimental)"
  type        = string
  default     = "ghcr.io/xtdb/xtdb:latest"
}

# ═══════════════════════════════════════════════════════════════════════════
# Observability Configuration
# ═══════════════════════════════════════════════════════════════════════════

variable "loki_minio_endpoint" {
  description = "MinIO endpoint for Loki storage (host:port, no scheme)"
  type        = string
  default     = "127.0.0.1:9000"
}

variable "loki_minio_access_key" {
  description = "MinIO access key for Loki"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}

variable "loki_minio_secret_key" {
  description = "MinIO secret key for Loki"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}

variable "loki_bucket" {
  description = "S3 bucket for Loki storage"
  type        = string
  default     = "loki"
}

variable "grafana_admin_password" {
  description = "Grafana admin password"
  type        = string
  default     = "admin"
  sensitive   = true
}

variable "nomad_addr_for_alloy" {
  description = "Nomad HTTP address for Alloy metrics scrape"
  type        = string
  default     = "127.0.0.1:4646"
}

variable "alloy_prometheus_remote_write_url" {
  description = "Prometheus remote_write URL for Alloy (empty = derive from nomad_addr)"
  type        = string
  default     = ""
}

variable "alloy_loki_push_url" {
  description = "Loki push URL for Alloy (empty = derive from nomad_addr)"
  type        = string
  default     = ""
}

# ═══════════════════════════════════════════════════════════════════════════
# Notification Configuration
# ═══════════════════════════════════════════════════════════════════════════

variable "ntfy_base_url" {
  description = "ntfy base URL"
  type        = string
  default     = "http://aither.mb.sun.ac.za"
}

variable "ntfy_minio_endpoint" {
  description = "MinIO endpoint for ntfy attachments (host:port, no scheme)"
  type        = string
  default     = "100.70.185.46:9000"
}

variable "ntfy_minio_access_key" {
  description = "MinIO access key for ntfy"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}

variable "ntfy_minio_secret_key" {
  description = "MinIO secret key for ntfy"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}

variable "ntfy_attachment_bucket" {
  description = "S3 bucket for ntfy attachments"
  type        = string
  default     = "ntfy"
}

variable "job_notifier_nomad_addr" {
  description = "Nomad API address for job-notifier event stream"
  type        = string
  default     = "http://100.70.185.46:4646"
}

variable "job_notifier_ntfy_url" {
  description = "ntfy URL for job-notifier"
  type        = string
  default     = "http://100.70.185.46:8088"
}

variable "job_notifier_ntfy_topic" {
  description = "ntfy topic for job notifications"
  type        = string
  default     = "abc-jobs"
}

variable "job_notifier_nomad_token" {
  description = "Nomad token for job-notifier (empty = use nomad_token)"
  type        = string
  default     = ""
  sensitive   = true
}

# ═══════════════════════════════════════════════════════════════════════════
# Storage Configuration
# ═══════════════════════════════════════════════════════════════════════════

variable "rustfs_access_key" {
  description = "RustFS access key"
  type        = string
  default     = "rustfsadmin"
  sensitive   = true
}

variable "rustfs_secret_key" {
  description = "RustFS secret key"
  type        = string
  default     = "rustfsadmin"
  sensitive   = true
}

# ═══════════════════════════════════════════════════════════════════════════
# Experimental Service Configuration
# ═══════════════════════════════════════════════════════════════════════════

variable "postgres_db" {
  description = "Default database name for PostgreSQL (abc-experimental)"
  type        = string
  default     = "abc"
}

variable "postgres_user" {
  description = "PostgreSQL superuser name (abc-experimental)"
  type        = string
  default     = "abc"
}

variable "postgres_password" {
  description = "PostgreSQL superuser password (abc-experimental)"
  type        = string
  default     = "abc_db_secret"
  sensitive   = true
}

variable "restic_server_htpasswd" {
  description = <<-EOT
    htpasswd entry for the Restic REST server 'abc' user (abc-experimental).
    Must be a pre-hashed htpasswd line, e.g.:
      abc:$2y$10$...   (bcrypt — generate with: htpasswd -nB abc)
    The default corresponds to user=abc, password=restic_secret.
  EOT
  type      = string
  # Default hash for password "restic_secret" (bcrypt rounds=10).
  # Rotate by running: htpasswd -nB abc  and setting this variable.
  default   = "abc:$2y$10$UwAIqgfTb7jFiDrSrOD.9.WQVPIAhv5u5OwQ2qLJ5Bh3lcpU5KsKq"
  sensitive = true
}

variable "caddy_cluster_public_host" {
  description = "Public hostname Caddy serves (defaults to cluster_public_host)"
  type        = string
  default     = ""
}

variable "caddy_tailscale_ip" {
  description = "Tailscale IP Caddy listens on (defaults to cluster_tailscale_ip)"
  type        = string
  default     = ""
}

# ═══════════════════════════════════════════════════════════════════════════
# Experimental Service Configuration (continued) — XTDB v2
# ═══════════════════════════════════════════════════════════════════════════

variable "xtdb_node" {
  description = "Hostname of the cluster node to schedule XTDB on"
  type        = string
  default     = "aither"
}

variable "xtdb_http_port" {
  description = "Static host port for XTDB HTTP API (container always listens on 3000)"
  type        = number
  default     = 5555
}

variable "xtdb_postgres_url" {
  description = <<-EOT
    JDBC URL for XTDB's transaction log backend (optional).
    When empty (default), XTDB uses a local-disk txLog under /scratch/xtdb/txlog.
    When set, XTDB uses the Postgres module — the referenced database must exist
    before first startup and the user must have CREATE TABLE privileges.
    Example: "jdbc:postgresql://127.0.0.1:5432/xtdb?user=abc&password=abc_db_secret"
    (Requires enable_postgres=true; create the 'xtdb' db first via psql.)
  EOT
  type      = string
  default   = ""
  sensitive = true
}

# ═══════════════════════════════════════════════════════════════════════════
# Automations Tier — fx event-driven hook enable flags and configuration
#
# fx jobs run in abc-automations namespace.  Both default to true — they
# are core infrastructure for file lifecycle management (MinIO events and
# tusd post-finish rename).
# ═══════════════════════════════════════════════════════════════════════════

variable "enable_fx_notify" {
  description = "Deploy fx-notify (MinIO webhook → ntfy notification bridge)"
  type        = bool
  default     = true
}

variable "enable_fx_tusd_hook" {
  description = "Deploy fx-tusd-hook (tusd post-finish hook: rename S3 object + ntfy notification)"
  type        = bool
  default     = true
}

# ── fx-notify configuration ───────────────────────────────────────────────

variable "fx_notify_node" {
  description = "Hostname of the cluster node to schedule fx-notify on"
  type        = string
  default     = "aither"
}

variable "fx_notify_ntfy_url" {
  description = "ntfy topic URL that receives MinIO event notifications"
  type        = string
  default     = "http://ntfy.aither/minio-events"
}

# ── fx-tusd-hook configuration ────────────────────────────────────────────

variable "fx_tusd_hook_node" {
  description = "Hostname of the cluster node to schedule fx-tusd-hook on"
  type        = string
  default     = "nomad01"
}

variable "fx_tusd_hook_ntfy_url" {
  description = "ntfy topic URL for tusd upload-complete notifications"
  type        = string
  default     = "http://ntfy.aither/tusd-uploads"
}

variable "fx_tusd_hook_minio_endpoint" {
  description = "MinIO S3 API base URL for the hook to call (no trailing slash)"
  type        = string
  default     = "http://100.70.185.46:9000"
}

variable "fx_tusd_hook_minio_bucket" {
  description = "MinIO bucket where tusd stores in-progress uploads"
  type        = string
  default     = "tusd"
}

variable "fx_tusd_hook_s3_access_key" {
  description = "S3 / MinIO access key used by fx-tusd-hook for object rename operations"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}

variable "fx_tusd_hook_s3_secret_key" {
  description = "S3 / MinIO secret key used by fx-tusd-hook for object rename operations"
  type        = string
  default     = "minioadmin"
  sensitive   = true
}
