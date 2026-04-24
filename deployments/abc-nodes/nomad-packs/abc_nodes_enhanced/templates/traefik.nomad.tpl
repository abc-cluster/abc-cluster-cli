# Traefik reverse proxy — abc-nodes floor

job "abc-nodes-traefik" {
  namespace   = [[ var "abc_services_namespace" . | quote ]]
  region      = "global"
  datacenters = [[ var "datacenters" . | toStringList ]]
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "traefik"
  }

  group "traefik" {
    count = 1

    network {
      mode = "host"
      port "http" {
        static = 8081
      }
      port "dashboard" {
        static = 8888
      }
    }

    task "traefik" {
      driver = "raw_exec"

      config {
        command = "local/traefik"
        args    = ["--configFile=local/traefik.yml"]
      }

      artifact {
        source      = "https://github.com/traefik/traefik/releases/download/v[[ var "traefik_version" . ]]/traefik_v[[ var "traefik_version" . ]]_linux_amd64.tar.gz"
        destination = "local/"
      }

      template {
        data        = <<EOF
global:
  checkNewVersion: false
  sendAnonymousUsage: false

api:
  dashboard: true
  insecure: false
  basePath: /traefik

ping: {}

entryPoints:
  web:
    address: ":8081"
  traefik:
    address: ":8888"

providers:
  file:
    filename: local/routes.yml
    watch: true

log:
  level: INFO
EOF
        destination = "local/traefik.yml"
      }

      template {
        change_mode = "noop"
        data        = <<EOF
http:
  middlewares:
    nomad-auth:
      forwardAuth:
        address: "http://127.0.0.1:9191/auth"
        trustForwardHeader: true
        authResponseHeaders:
          - "X-Auth-User"
          - "X-Auth-Group"
          - "X-Auth-Namespace"

  routers:
    traefik-dashboard:
      entryPoints: ["web"]
      rule: "Host(`[[ var "cluster_public_host" . ]]`) && PathPrefix(`/traefik`)"
      service: api@internal
      priority: 1
    grafana:
      entryPoints: ["web"]
      rule: "Host(`grafana.aither`)"
      service: grafana
    grafana-alloy:
      entryPoints: ["web"]
      rule: "Host(`grafana-alloy.aither`)"
      service: grafana-alloy
    loki:
      entryPoints: ["web"]
      rule: "Host(`loki.aither`)"
      service: loki
    minio-s3:
      entryPoints: ["web"]
      rule: "Host(`minio.aither`)"
      service: minio-s3
    minio-console:
      entryPoints: ["web"]
      rule: "Host(`minio-console.aither`)"
      service: minio-console
    ntfy:
      entryPoints: ["web"]
      rule: "Host(`ntfy.aither`)"
      service: ntfy
    prometheus:
      entryPoints: ["web"]
      rule: "Host(`prometheus.aither`)"
      service: prometheus
    rustfs:
      entryPoints: ["web"]
      rule: "Host(`rustfs.aither`)"
      service: rustfs
    tusd:
      entryPoints: ["web"]
      rule: "Host(`tusd.aither`)"
      middlewares:
        - nomad-auth
      service: tusd
    uppy:
      entryPoints: ["web"]
      rule: "Host(`uppy.aither`)"
      service: uppy

  services:
    grafana:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:3000"
    grafana-alloy:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:12345"
    loki:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:3100"
    minio-s3:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:9000"
    minio-console:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:9001"
    ntfy:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:8088"
    prometheus:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:9090"
    rustfs:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:9900"
    tusd:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:8080"
    uppy:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:8090"
EOF
        destination = "local/routes.yml"
      }

      resources {
        cpu    = 256
        memory = 256
      }

      service {
        name     = "abc-nodes-traefik"
        port     = "http"
        provider = "nomad"
        tags     = ["abc-nodes", "traefik", "proxy"]
      }

      service {
        name     = "abc-nodes-traefik-dashboard"
        port     = "dashboard"
        provider = "nomad"
        tags     = ["abc-nodes", "traefik", "dashboard"]
      }
    }
  }
}
