# Grafana (dashboards) — abc-nodes floor
# Default: Grafana data in the container (ephemeral).

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "grafana_image" {
  type    = string
  default = "grafana/grafana:11.4.0"
}

variable "grafana_admin_password" {
  type    = string
  default = "admin"
}

job "abc-nodes-grafana" {
  region      = "global"
  datacenters = var.datacenters
  type        = "service"

  meta {
    abc_cluster_type = "abc-nodes"
    service          = "grafana"
  }

  group "grafana" {
    count = 1

    network {
      mode = "bridge"
      port "http" {
        static = 3000
        to     = 3000
      }
    }

    task "grafana" {
      driver = "containerd-driver"

      config {
        image = var.grafana_image
      }

      env {
        GF_SECURITY_ADMIN_PASSWORD = var.grafana_admin_password
        GF_SERVER_HTTP_PORT        = "3000"
        GF_PATHS_PROVISIONING      = "/local/provisioning"
      }

      template {
        data        = <<EOF
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    url: http://100.70.185.46:9090
    access: proxy
    isDefault: true
    editable: false

  # Must match abc-nodes-loki common.path_prefix (/loki); otherwise Grafana calls
  # /api/v1/... on :3100 and the datasource looks "unreachable".
  - name: Loki
    type: loki
    uid: loki
    url: http://100.70.185.46:3100/loki
    access: proxy
    isDefault: false
    editable: false
    jsonData:
      maxLines: 1000
EOF
        destination = "local/provisioning/datasources/default.yaml"
      }

      # Provisioned dashboard: Nomad alloc stdout/stderr (labels from Alloy).
      template {
        data        = <<EOF
apiVersion: 1
providers:
  - name: abc-nodes
    orgId: 1
    folder: ABC Nodes
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    allowUiUpdates: true
    options:
      path: /local/provisioning/dashboards/files
EOF
        destination = "local/provisioning/dashboards/dashboard.yaml"
      }

      template {
        destination = "local/provisioning/dashboards/files/nomad-loki-logs.json"
        data        = <<EOF
{
  "annotations": { "list": [] },
  "editable": true,
  "fiscalYearStartMonth": 0,
  "graphTooltip": 0,
  "links": [],
  "liveNow": false,
  "panels": [
    {
      "datasource": { "type": "loki", "uid": "loki" },
      "gridPos": { "h": 22, "w": 24, "x": 0, "y": 0 },
      "id": 1,
      "options": {
        "dedupStrategy": "none",
        "enableLogDetails": true,
        "prettifyLogMessage": false,
        "showCommonLabels": false,
        "showLabels": true,
        "showTime": true,
        "sortOrder": "Descending",
        "wrapLogMessage": true
      },
      "targets": [
        {
          "datasource": { "type": "loki", "uid": "loki" },
          "editorMode": "code",
          "expr": "{stream=~\"stdout|stderr\"}",
          "queryType": "range",
          "refId": "A"
        }
      ],
      "title": "Nomad allocation logs (Alloy file tail → Loki)",
      "type": "logs"
    }
  ],
  "refresh": "30s",
  "schemaVersion": 39,
  "tags": ["abc-nodes", "nomad", "loki"],
  "templating": { "list": [] },
  "time": { "from": "now-3h", "to": "now" },
  "timepicker": {},
  "timezone": "",
  "title": "Nomad allocation logs",
  "uid": "abc-nodes-nomad-loki-logs",
  "version": 1,
  "weekStart": ""
}
EOF
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name     = "abc-nodes-grafana"
        port     = "http"
        provider = "nomad"
        tags = [
          "abc-nodes", "grafana", "ui",
          "traefik.enable=true",
          "traefik.http.routers.grafana.rule=Host(`grafana.aither`)",
          "traefik.http.services.grafana.loadbalancer.server.port=3000",
        ]
      }
    }
  }
}
