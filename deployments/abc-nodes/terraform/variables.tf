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

variable "enable_garage" {
  description = "Deploy Garage S3-compatible storage (long-term archive + backup tier behind RustFS)"
  type        = bool
  default     = true
}

variable "enable_docs" {
  description = "Deploy the docs static-site job (serves Docusaurus build at http://docs.aither)"
  type        = bool
  default     = true
}

variable "enable_caddy_tailscale" {
  description = "Deploy the unified Caddy gateway (binds port 80 on both Tailscale and LAN IPs; owns *.aither vhosts and the LAN landing page). Active production gateway."
  type        = bool
  default     = true
}

variable "enable_abc_backups" {
  description = "Deploy the periodic restic-on-Garage backup job (Consul/Vault/Nomad snapshots)"
  type        = bool
  default     = true
}

variable "enable_fx_archive" {
  description = "Deploy fx-archive (periodic age-based RustFS → Garage tier-down)"
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
  description = "Deploy local OCI registry (registry:2) on the abc-experimental namespace — push your laptop-built images and pull them in Nomad jobs. Persists on aither's scratch volume."
  type        = bool
  default     = false
}

variable "docker_registry_image" {
  description = "OCI image for the local registry"
  type        = string
  default     = "registry:2"
}

variable "docker_registry_node" {
  description = "Hostname constraint for the local registry (single-node — data is on this node's scratch volume)"
  type        = string
  default     = "aither"
}

variable "docker_registry_port" {
  description = "Host port mapped to the registry container's :5000. Clients push/pull at <cluster_tailscale_ip>:<this>."
  type        = number
  default     = 5000
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
  description = "DEPRECATED — superseded by enable_abc_backups (restic-on-Garage). Deploy Restic REST server for cluster backups (abc-experimental). Local scratch repo only, no compression or replication; kept for reference."
  type        = bool
  default     = false
}

variable "enable_caddy" {
  description = "DEPRECATED — superseded by enable_caddy_tailscale. Old ACME-focused Caddy (abc-experimental). Both jobs would conflict on port 80; do not enable simultaneously."
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

# ─── Supabase stack ─────────────────────────────────────────────────────────
#
# The supabase stack ports the upstream docker-compose to one Nomad job
# (`abc-experimental-supabase`). Defaults below are the well-known INSECURE
# values from supabase's own .env.example — they work out of the box for a
# demo / experimental tier but MUST be replaced before any data goes in.
#
# To regenerate proper secrets:
#   - JWT_SECRET: any random ≥32 char string
#   - ANON_KEY / SERVICE_ROLE_KEY: HS256-signed JWTs over the JWT_SECRET
#     with role=anon and role=service_role respectively. Use
#     supabase's own utility (sh ./utils/generate-keys.sh in their repo)
#     or the docs at https://supabase.com/docs/guides/self-hosting

variable "supabase_node" {
  description = "Hostname constraint for the supabase stack (single-node)"
  type        = string
  default     = "aither"
}

variable "supabase_db_image" {
  description = "supabase/postgres image — has the auth/storage/realtime/pg_net/pgsodium extensions baked in. Cannot be replaced with vanilla postgres."
  type        = string
  default     = "supabase/postgres:15.8.1.085"
}

variable "supabase_studio_image" {
  description = "Supabase Studio (Dashboard UI) image"
  type        = string
  default     = "supabase/studio:latest"
}

variable "supabase_meta_image" {
  description = "postgres-meta image (Studio's schema introspection backend)"
  type        = string
  default     = "supabase/postgres-meta:v0.96.3"
}

variable "supabase_auth_image" {
  description = "GoTrue (auth API) image"
  type        = string
  default     = "supabase/gotrue:v2.186.0"
}

variable "supabase_rest_image" {
  description = "PostgREST image (REST API over postgres + RLS)"
  type        = string
  default     = "postgrest/postgrest:v14.8"
}

variable "supabase_kong_image" {
  description = "Kong API gateway image"
  type        = string
  default     = "kong/kong:3.9.1"
}

variable "kong_http_port" {
  description = "Host port mapped to Kong's container port 8000 (the only externally-exposed port for the supabase stack)"
  type        = number
  default     = 8000
}

variable "supabase_postgres_db" {
  description = "Default database name for the supabase postgres instance (NOT the same as var.postgres_db, which is for the standalone abc-experimental-postgres job)"
  type        = string
  default     = "postgres"
}

variable "supabase_postgres_password" {
  description = "Postgres superuser password for the supabase stack (UNSAFE default — replace before exposing anywhere)"
  type        = string
  default     = "this-is-an-experimental-password-replace-it"
  sensitive   = true
}

variable "supabase_jwt_secret" {
  description = "Symmetric (HS256) JWT signing secret. Must be ≥32 chars and shared across auth/rest/realtime/storage. The default below matches the published anon/service_role keys."
  type        = string
  default     = "your-super-secret-jwt-token-with-at-least-32-characters-long"
  sensitive   = true
}

variable "supabase_jwt_exp" {
  description = "JWT expiration time in seconds"
  type        = number
  default     = 3600
}

variable "supabase_anon_key" {
  description = "Pre-signed HS256 JWT for the 'anon' role over var.supabase_jwt_secret. Embedded in client-side code."
  type        = string
  default     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyAgCiAgICAicm9sZSI6ICJhbm9uIiwKICAgICJpc3MiOiAic3VwYWJhc2UtZGVtbyIsCiAgICAiaWF0IjogMTY0MTc2OTIwMCwKICAgICJleHAiOiAxNzk5NTM1NjAwCn0.dc_X5iR_VP_qT0zsiyj_I_OZ2T9FtRU2BBNWN8Bu4GE"
  sensitive   = true
}

variable "supabase_service_role_key" {
  description = "Pre-signed HS256 JWT for the 'service_role' role over var.supabase_jwt_secret. Server-side ONLY — never embed in client code."
  type        = string
  default     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyAgCiAgICAicm9sZSI6ICJzZXJ2aWNlX3JvbGUiLAogICAgImlzcyI6ICJzdXBhYmFzZS1kZW1vIiwKICAgICJpYXQiOiAxNjQxNzY5MjAwLAogICAgImV4cCI6IDE3OTk1MzU2MDAKfQ.DaYlNEoUrrEn2Ig7tqibS-PHK5vgusbcbo7X36XVt4Q"
  sensitive   = true
}

variable "supabase_dashboard_username" {
  description = "Studio dashboard basic-auth username (Kong-side)"
  type        = string
  default     = "supabase"
}

variable "supabase_dashboard_password" {
  description = "Studio dashboard basic-auth password (UNSAFE default — replace)"
  type        = string
  default     = "this_password_is_insecure_and_should_be_updated"
  sensitive   = true
}

variable "supabase_pg_meta_crypto_key" {
  description = "AES-256 key (≥32 chars) used by postgres-meta to encrypt stored connection strings"
  type        = string
  default     = "this-is-an-experimental-32-char-key"
  sensitive   = true
}

variable "supabase_public_url" {
  description = "Externally-reachable URL for the supabase stack (used by auth for OAuth callbacks and by studio for API references)"
  type        = string
  default     = "http://supabase.aither"
}

variable "supabase_site_url" {
  description = "Site URL gate-list passed to GoTrue (auth) — used to validate post-login redirects"
  type        = string
  default     = "http://supabase.aither"
}

variable "supabase_disable_signup" {
  description = "Set to \"true\" to lock down GoTrue and require an admin to invite users"
  type        = string
  default     = "false"
}

variable "supabase_pgrst_schemas" {
  description = "Comma-separated postgres schemas exposed via PostgREST"
  type        = string
  default     = "public,storage,graphql_public"
}

variable "supabase_default_org_name" {
  description = "Default organization name shown in Studio"
  type        = string
  default     = "Default Organization"
}

variable "supabase_default_project_name" {
  description = "Default project name shown in Studio"
  type        = string
  default     = "Default Project"
}

variable "supabase_enable_optional_integrations" {
  description = <<-DESC
    When true (default), db-init runs CREATE EXTENSION for the optional
    integrations bundled with the supabase/postgres image:
      - pg_cron       (Studio: Database / Cron Jobs)
      - pgmq          (Studio: Database / Queues)
      - pg_net        (Studio: Database / Webhooks; needed for the
                       supabase_functions.http_request trigger)
      - vector        (pgvector — embeddings / AI workloads)
      - pg_jsonschema (JSON schema validation in CHECK constraints)
      - hstore, citext (small, broadly-useful schema types)
    Set to "false" to keep only the minimum (auth/storage/realtime baseline
    that the supabase/postgres image creates on first boot).
  DESC
  type        = string
  default     = "true"
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
  description = "Caddy container image (abc-experimental, used by the OLD nomad_job.caddy ACME job)"
  type        = string
  default     = "caddy:2-alpine"
}

# ── caddy-tailscale (unified gateway) configuration ──────────────────────────
# Surfaces the four landing-page / routing variables exposed by
# nomad/experimental/caddy-tailscale.nomad.hcl.  Defaults match production.

variable "caddy_tailscale_service_domain" {
  description = "Subdomain base for cluster service vhosts (e.g. \"aither\" → grafana.aither, rustfs.aither, garage.aither)"
  type        = string
  default     = "aither"
}

variable "caddy_tailscale_lan_host" {
  description = "Institutional LAN hostname users hit on the wired network"
  type        = string
  default     = "aither.mb.sun.ac.za"
}

variable "caddy_tailscale_lan_ip" {
  description = "Institutional LAN IPv4 of aither (Caddy binds port 80 here in addition to the Tailscale IP)"
  type        = string
  default     = "146.232.174.77"
}

variable "caddy_tailscale_ts_ip" {
  description = "Tailscale IPv4 of aither (Caddy binds port 80 here; *.aither split-DNS resolves to this address)"
  type        = string
  default     = "100.70.185.46"
}

variable "docs_caddy_image" {
  description = "Caddy image used by the docs static-site job (abc-nodes-docs)"
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

# ── Garage configuration ───────────────────────────────────────────────────
# Garage is the long-term archive + backup tier (zstd compression + dedup
# + future geo-replication).  Most secrets are randomly generated by
# random_password resources in main.tf — these vars only cover knobs that
# need to be visible to operators.

variable "garage_image" {
  description = "Garage container image (Deuxfleurs official)"
  type        = string
  default     = "dxflrs/garage:v1.1.0"
}

variable "garage_webui_image" {
  description = "Garage Web UI container image (community-maintained admin UI)"
  type        = string
  default     = "khairul169/garage-webui:latest"
}

variable "garage_node_capacity" {
  description = "Capacity advertised to Garage layout for aither's data dir"
  type        = string
  default     = "100G"
}

variable "garage_zone" {
  description = "Garage layout zone — used for replica placement when more nodes are added"
  type        = string
  default     = "dc1"
}

variable "garage_restic_access_key" {
  description = "Garage S3 access key for the restic backup repo (deterministic — stored in state)"
  type        = string
  default     = "GKADMIN000000000000RESTIC"
}

variable "garage_archive_access_key" {
  description = "Garage S3 access key for the fx-archive tier-down job (deterministic — stored in state)"
  type        = string
  default     = "GKADMIN00000000000ARCHIVE"
}

variable "garage_internal_endpoint" {
  description = "In-cluster S3 endpoint URL for Garage (consumed by abc-backups + fx-archive)"
  type        = string
  default     = "http://abc-nodes-garage-s3.service.consul:3900"
}

variable "garage_backup_bucket" {
  description = "Garage bucket for restic snapshots"
  type        = string
  default     = "cluster-backups"
}

# ── abc-backups configuration ─────────────────────────────────────────────

variable "backups_schedule_cron" {
  description = "Cron schedule for abc-backups (UTC)"
  type        = string
  default     = "30 2 * * *"
}

variable "backups_keep_daily" {
  description = "Number of daily restic snapshots to retain"
  type        = number
  default     = 7
}

variable "backups_keep_weekly" {
  description = "Number of weekly restic snapshots to retain"
  type        = number
  default     = 4
}

variable "backups_keep_monthly" {
  description = "Number of monthly restic snapshots to retain"
  type        = number
  default     = 12
}

variable "backups_consul_addr" {
  description = "Consul HTTP API endpoint for snapshot capture"
  type        = string
  default     = "http://100.70.185.46:8500"
}

variable "backups_consul_token" {
  description = "Consul token with snapshot:write capability (set via tfvars; empty disables)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "backups_vault_addr" {
  description = "Vault HTTP API endpoint for raft snapshot capture"
  type        = string
  default     = "http://100.70.185.46:8200"
}

variable "backups_vault_token" {
  description = "Vault token with sudo on sys/storage/raft/snapshot (set via tfvars; empty disables vault snapshot)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "backups_nomad_addr" {
  description = "Nomad HTTP API endpoint for job-spec capture"
  type        = string
  default     = "http://100.70.185.46:4646"
}

variable "backups_ntfy_url" {
  description = "ntfy topic URL for backup completion notifications"
  type        = string
  default     = "http://ntfy.aither/backups"
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

variable "xtdb_healthz_port" {
  description = "Static host port for XTDB healthz endpoint (minimal HTTP liveness probe — the only HTTP interface in the standalone image)"
  type        = number
  default     = 5555
}

variable "xtdb_pgwire_port" {
  description = "Static host port for XTDB Postgres wire protocol (pgwire) — primary query interface; connect with psql or any JDBC driver"
  type        = number
  default     = 15432
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
  description = "Hostname of the cluster node to schedule fx-tusd-hook on (must have a local Consul agent; only 'aither' qualifies on this cluster)"
  type        = string
  default     = "aither"
}

variable "fx_tusd_hook_ntfy_url" {
  description = "ntfy topic URL for tusd upload-complete notifications"
  type        = string
  default     = "http://ntfy.aither/tusd-uploads"
}

variable "fx_tusd_hook_minio_endpoint" {
  description = "S3 API base URL the tusd hook signs requests against (no trailing slash). Variable name kept for back-compat; default points at RustFS now that MinIO is retired."
  type        = string
  default     = "http://abc-nodes-rustfs-s3.service.consul:9900"
}

variable "fx_tusd_hook_minio_bucket" {
  description = "S3 bucket where tusd stores in-progress uploads (RustFS, with archival to Garage via fx-archive)"
  type        = string
  default     = "tusd"
}

variable "fx_tusd_hook_s3_access_key" {
  description = "S3 access key used by fx-tusd-hook for object rename operations (RustFS admin creds)"
  type        = string
  default     = "rustfsadmin"
  sensitive   = true
}

variable "fx_tusd_hook_s3_secret_key" {
  description = "S3 secret key used by fx-tusd-hook for object rename operations (RustFS admin creds)"
  type        = string
  default     = "rustfsadmin"
  sensitive   = true
}

# ── fx-archive configuration ───────────────────────────────────────────────

variable "fx_archive_node" {
  description = "Hostname of the cluster node to schedule fx-archive on (must reach RustFS + Garage)"
  type        = string
  default     = "aither"
}

variable "fx_archive_rustfs_endpoint" {
  description = "In-cluster RustFS S3 endpoint that fx-archive reads from"
  type        = string
  default     = "http://abc-nodes-rustfs-s3.service.consul:9900"
}

variable "fx_archive_dest_bucket" {
  description = "Destination bucket on Garage for tier-down (created by garage bootstrap)"
  type        = string
  default     = "archive"
}

variable "fx_archive_source_buckets" {
  description = "Comma-separated RustFS bucket names to tier into Garage's archive bucket"
  type        = string
  default     = "tusd"
}

variable "fx_archive_age_days" {
  description = "Only RustFS objects older than this are eligible for tier-down"
  type        = number
  default     = 30
}

variable "fx_archive_delete_after_copy" {
  description = "If true, delete from RustFS after a verified copy to Garage. Off by default — flip on once the archive is trusted."
  type        = bool
  default     = false
}

variable "fx_archive_ntfy_url" {
  description = "ntfy topic URL for fx-archive run summaries"
  type        = string
  default     = "http://ntfy.aither/archive"
}

variable "fx_archive_schedule_cron" {
  description = "Cron schedule for fx-archive (UTC)"
  type        = string
  default     = "0 3 * * *"
}
