# ── Shared (MinIO + tusd) ─────────────────────────────────────────────────────

variable "datacenters" {
  description = "Nomad datacenters eligible for placement"
  type        = list(string)
  default     = ["dc1", "default"]
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
  description = "Loki base URL for Grafana datasource provisioning"
  type        = string
  default     = "http://127.0.0.1:3100"
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
  type    = string
  default = "http://127.0.0.1:9090/api/v1/write"
}

variable "alloy_loki_push_url" {
  type    = string
  default = "http://127.0.0.1:3100/loki/api/v1/push"
}

variable "nomad_alloc_log_path" {
  type        = string
  description = "Glob path to Nomad allocation log files on the host"
  default     = "/var/lib/nomad/alloc/*/alloc/logs/*.std*.*"
}
