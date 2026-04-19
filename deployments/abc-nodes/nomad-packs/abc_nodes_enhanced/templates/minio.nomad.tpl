# MinIO — abc-nodes enhanced pack

job "abc-nodes-minio" {
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "minio"
  }

  group "minio" {
    count = 1

    network {
      mode = "bridge"
      port "api" {
        static = 9000
        to     = 9000
      }
      port "console" {
        static = 9001
        to     = 9001
      }
    }

    task "minio" {
      driver = "containerd-driver"

      config {
        image = [[ var "minio_image" . | quote ]]
        args = [
          "server", "/data",
          "--console-address", ":9001",
        ]
      }

      env {
        MINIO_ROOT_USER     = [[ var "minio_root_user" . | quote ]]
        MINIO_ROOT_PASSWORD = [[ var "minio_root_password" . | quote ]]
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-minio-s3"
        port     = "api"
        provider = "nomad"
        tags = [
          "abc-nodes", "minio", "s3",
          "traefik.enable=true",
          "traefik.http.routers.minio-s3.rule=Host(`minio.aither`)",
          "traefik.http.routers.minio-s3.service=minio-s3",
          "traefik.http.services.minio-s3.loadbalancer.server.port=9000",
        ]
      }

      service {
        name     = "abc-nodes-minio-console"
        port     = "console"
        provider = "nomad"
        tags = [
          "abc-nodes", "minio", "console",
          "traefik.enable=true",
          "traefik.http.routers.minio-console.rule=Host(`minio-console.aither`)",
          "traefik.http.routers.minio-console.service=minio-console",
          "traefik.http.services.minio-console.loadbalancer.server.port=9001",
        ]
      }
    }
  }
}
