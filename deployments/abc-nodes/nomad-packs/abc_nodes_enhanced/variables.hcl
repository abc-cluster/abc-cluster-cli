# ── Shared (MinIO + tusd) ─────────────────────────────────────────────────────

variable "datacenters" {
  description = "Nomad datacenters eligible for placement"
  type        = list(string)
  default     = ["dc1", "default"]
}

variable "abc_services_namespace" {
  description = "Namespace used by core abc-nodes services using the abc-services convention"
  type        = string
  default     = "abc-services"
}


variable "applications_namespace" {
  description = "Namespace used by application-facing jobs like Uppy"
  type        = string
  default     = "abc-applications"
}

variable "minio_image" {
  type    = string
  default = "minio/minio:RELEASE.2024-12-18T13-15-44Z"
}

variable "minio_root_user" {
  type    = string
  default = "minioadmin"
}

variable "minio_root_password" {
  type    = string
  default = "minioadmin"
}

variable "tusd_image" {
  type    = string
  default = "tusproject/tusd:v2.4.0"
}

variable "minio_s3_endpoint" {
  description = "MinIO S3 API base URL for tusd (no trailing slash)"
  type        = string
  default     = "http://127.0.0.1:9000"
}

variable "s3_disable_content_hashes" {
  type    = bool
  default = true
}

variable "s3_bucket" {
  type    = string
  default = "tusd"
}

variable "s3_access_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_region" {
  type    = string
  default = "us-east-1"
}

# ── Prometheus ────────────────────────────────────────────────────────────────

variable "prometheus_image" {
  type    = string
  default = "prom/prometheus:v2.54.1"
}

# ── Loki (S3 backend = same MinIO; host:port without scheme) ─────────────────

variable "loki_image" {
  type    = string
  default = "grafana/loki:3.3.2"
}

variable "loki_minio_endpoint" {
  description = "MinIO host:port for Loki storage (no scheme), e.g. 127.0.0.1:9000"
  type        = string
  default     = "127.0.0.1:9000"
}

variable "loki_minio_access_key" {
  type    = string
  default = "minioadmin"
}

variable "loki_minio_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "loki_bucket" {
  type    = string
  default = "loki"
}

# ── Grafana ───────────────────────────────────────────────────────────────────

variable "grafana_image" {
  type    = string
  default = "grafana/grafana:11.4.0"
}

variable "grafana_admin_password" {
  type    = string
  default = "admin"
}

variable "grafana_prometheus_url" {
  description = "Prometheus base URL for Grafana datasource provisioning"
  type        = string
  default     = "http://127.0.0.1:9090"
}

variable "grafana_loki_url" {
  description = "Loki base URL for Grafana (must include path_prefix /loki when Loki uses common.path_prefix)"
  type        = string
  default     = "http://127.0.0.1:3100/loki"
}

# ── Grafana Alloy (raw_exec on host) ─────────────────────────────────────────

variable "alloy_version" {
  type    = string
  default = "1.15.1"
}

variable "nomad_addr" {
  description = "Nomad HTTP address host:port for metrics scrape (reachable from host network)"
  type        = string
  default     = "127.0.0.1:4646"
}

variable "nomad_token" {
  type        = string
  description = "Nomad ACL token for /v1/metrics scrape (lab default is unsafe; override in production)"
  default     = ""
}

variable "alloy_prometheus_remote_write_url" {
  description = "Prometheus remote_write URL; empty = derived from nomad_addr host + :9090"
  type        = string
  default     = ""
}

variable "alloy_loki_push_url" {
  description = "Loki push URL; empty = derived from nomad_addr host + :3100/loki/api/v1/push"
  type        = string
  default     = ""
}

# ── Traefik / ForwardAuth / ntfy / Uppy ─────────────────────────────────────

variable "traefik_version" {
  type    = string
  default = "3.3.5"
}

variable "cluster_public_host" {
  description = "Public host used for the Traefik dashboard route"
  type        = string
  default     = "aither.mb.sun.ac.za"
}

variable "auth_namespace" {
  description = "Namespace for abc-nodes-auth"
  type        = string
  default     = "abc-services"
}

variable "auth_nomad_addr" {
  description = "Nomad API address used by abc-nodes-auth to validate tokens"
  type        = string
  default     = "http://100.70.185.46:4646"
}

variable "ntfy_image" {
  type    = string
  default = "binwiederhier/ntfy:v2.11.0"
}

variable "ntfy_base_url" {
  type    = string
  default = "http://aither.mb.sun.ac.za"
}

variable "ntfy_minio_endpoint" {
  description = "MinIO host:port without scheme for ntfy attachment storage"
  type        = string
  default     = "100.70.185.46:9000"
}

variable "ntfy_minio_access_key" {
  type    = string
  default = "minioadmin"
}

variable "ntfy_minio_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "ntfy_attachment_bucket" {
  type    = string
  default = "ntfy"
}

variable "nginx_image" {
  type    = string
  default = "nginx:1.27-alpine"
}

variable "tusd_endpoint" {
  description = "Browser-reachable TUS endpoint for the Uppy dashboard"
  type        = string
  default     = "http://aither.mb.sun.ac.za/services/tusd/files/"
}

variable "uppy_max_file_size_mb" {
  type    = number
  default = 5120
}

# ── RustFS / support services / notifier ────────────────────────────────────

variable "rustfs_image" {
  type    = string
  default = "rustfs/rustfs:latest"
}

variable "rustfs_access_key" {
  type    = string
  default = "rustfsadmin"
}

variable "rustfs_secret_key" {
  type    = string
  default = "rustfsadmin"
}

variable "docker_registry_image" {
  type    = string
  default = "registry:2"
}

variable "job_notifier_nomad_addr" {
  type    = string
  default = "http://100.70.185.46:4646"
}

variable "job_notifier_ntfy_url" {
  type    = string
  default = "http://100.70.185.46:8088"
}

variable "job_notifier_ntfy_topic" {
  type    = string
  default = "abc-jobs"
}
