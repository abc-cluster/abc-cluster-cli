# Loki (logs) — abc-nodes floor (single-process dev-style config)
# Storage: MinIO S3 backend for chunks; in-memory ring for single-node.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "loki_image" {
  type    = string
  default = "grafana/loki:3.3.2"
}

variable "minio_endpoint" {
  type        = string
  description = "MinIO host:port without scheme, e.g. 100.70.185.46:9000"
  default     = "100.70.185.46:9000"
}

variable "minio_access_key" {
  type    = string
  default = "minioadmin"
}

variable "minio_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "loki_bucket" {
  type    = string
  default = "loki"
}

job "abc-nodes-loki" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "loki"
  }

  group "loki" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 3100
        to     = 3100
      }
    }

    task "loki" {
      driver = "containerd-driver"

      config {
        image = var.loki_image
        args = [
          "-config.file=/local/loki.yaml",
        ]
      }

      template {
        data = <<EOF
auth_enabled: false

server:
  http_listen_port: 3100
  grpc_listen_port: 9095

common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    instance_addr: 127.0.0.1
    kvstore:
      store: inmemory

schema_config:
  configs:
    - from: 2020-10-24
      store: tsdb
      object_store: s3
      schema: v13
      index:
        prefix: index_
        period: 24h

storage_config:
  tsdb_shipper:
    active_index_directory: /loki/tsdb-index
    cache_location: /loki/tsdb-cache
  aws:
    bucketnames: ${var.loki_bucket}
    endpoint: ${var.minio_endpoint}
    access_key_id: ${var.minio_access_key}
    secret_access_key: ${var.minio_secret_key}
    insecure: true
    s3forcepathstyle: true
    region: us-east-1

limits_config:
  reject_old_samples: true
  reject_old_samples_max_age: 168h
EOF
        destination = "local/loki.yaml"
      }

      resources {
        cpu    = 500
        memory = 1536
      }

      service {
        name     = "abc-nodes-loki"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "loki", "logs",
          "traefik.enable=true",
          "traefik.http.routers.loki.rule=Host(`loki.aither`)",
          "traefik.http.services.loki.loadbalancer.server.port=3100",
        ]
      }
    }
  }
}
