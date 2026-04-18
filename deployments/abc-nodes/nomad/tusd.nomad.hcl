# tusd (resumable uploads) backed by S3-compatible storage — abc-nodes floor
# Set minio_s3_endpoint to the MinIO S3 API base URL reachable from the allocation
# (e.g. http://<node-ip>:<dynamic-host-port> or via mesh / LB).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "tusd_image" {
  type    = string
  default = "tusproject/tusd:v2.4.0"
}

variable "minio_s3_endpoint" {
  type        = string
  description = "S3 endpoint for tusd, e.g. http://10.0.0.5:30241"
  default     = "http://127.0.0.1:9000"
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

job "abc-nodes-tusd" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "tusd"
  }

  group "tusd" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        to = 8080
      }
    }

    task "tusd" {
      driver = "containerd-driver"

      config {
        image = var.tusd_image
        args = [
          "-s3-bucket", var.s3_bucket,
          "-s3-endpoint", var.minio_s3_endpoint,
          "-s3-disable-ssl",
          "-port", "8080",
          "-base-path", "/files/",
        ]
      }

      env {
        AWS_ACCESS_KEY_ID     = var.s3_access_key
        AWS_SECRET_ACCESS_KEY = var.s3_secret_key
        AWS_REGION            = var.s3_region
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-tusd"
        port     = "http"
        provider = "nomad"
        tags     = ["abc-nodes", "tusd", "http"]
      }
    }
  }
}
