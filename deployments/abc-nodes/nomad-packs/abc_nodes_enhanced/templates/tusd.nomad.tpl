# tusd — abc-nodes enhanced pack

job "abc-nodes-tusd" {
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
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
        static = 8080
        to     = 8080
      }
    }

    task "tusd" {
      driver = "containerd-driver"

      config {
        image = [[ var "tusd_image" . | quote ]]
[[ if var "s3_disable_content_hashes" . ]]
        args = [
          "-s3-bucket", [[ var "s3_bucket" . | quote ]],
          "-s3-endpoint", [[ trimSuffix "/" (trim (var "minio_s3_endpoint" .)) | quote ]],
          "-s3-disable-ssl",
          "-port", "8080",
          "-base-path", "/files/",
          "-s3-disable-content-hashes",
        ]
[[ else ]]
        args = [
          "-s3-bucket", [[ var "s3_bucket" . | quote ]],
          "-s3-endpoint", [[ trimSuffix "/" (trim (var "minio_s3_endpoint" .)) | quote ]],
          "-s3-disable-ssl",
          "-port", "8080",
          "-base-path", "/files/",
        ]
[[ end ]]
      }

      env {
        AWS_ACCESS_KEY_ID     = [[ var "s3_access_key" . | quote ]]
        AWS_SECRET_ACCESS_KEY = [[ var "s3_secret_key" . | quote ]]
        AWS_REGION            = [[ var "s3_region" . | quote ]]
      }

      resources {
        cpu    = 256
        memory = 512
      }

      service {
        name     = "abc-nodes-tusd"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "tusd", "http",
          "traefik.enable=true",
          "traefik.http.routers.tusd.rule=Host(`tusd.aither`)",
          "traefik.http.services.tusd.loadbalancer.server.port=8080",
        ]
      }
    }
  }
}
