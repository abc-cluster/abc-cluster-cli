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
  description = "MinIO S3 API base URL (no trailing slash), reachable from the tusd allocation"
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

variable "nginx_image" {
  type    = string
  default = "nginx:1.27-alpine"
}

variable "tusd_endpoint" {
  description = "Browser-reachable TUS endpoint for the Uppy dashboard"
  type        = string
  default     = "http://127.0.0.1:8080/files/"
}

variable "uppy_max_file_size_mb" {
  type    = number
  default = 5120
}
