# Grafana (dashboards) — abc-nodes floor
#
# DATA PERSISTENCE
# ─────────────────
#  Grafana home directory at /opt/nomad/scratch/grafana-data (scratch volume).
#  Dashboards, users, and settings survive restarts.
#
# CREDENTIALS (Nomad Variables, namespace: services)
# ───────────────────────────────────────────────────
#  Path: nomad/jobs/abc-nodes-grafana
#  Keys: admin_password
#
#  Store / rotate:
#    abc admin services nomad cli -- var put -namespace services -force \
#      nomad/jobs/abc-nodes-grafana admin_password=<password>
#
#  Falls back to the HCL variable default if the Variable is not set.

variable "datacenters" {
  type    = list(string)
  default = ["dc1", "default"]
}

variable "grafana_image" {
  type    = string
  default = "grafana/grafana:11.4.0"
}

variable "grafana_admin_password" {
  type        = string
  default     = "admin"
  description = "Fallback only — override via Nomad Variable nomad/jobs/abc-nodes-grafana"
}

job "abc-nodes-grafana" {
  namespace   = "services"
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

    volume "scratch" {
      type      = "host"
      read_only = false
      source    = "scratch"
    }

    # Grafana runs as UID 472; the host scratch dir is root-owned.
    # This prestart task creates the data dir and sets the right ownership.
    task "grafana-init" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
      driver = "raw_exec"
      config {
        command = "/bin/sh"
        args    = ["-c", "mkdir -p /opt/nomad/scratch/grafana-data && chown -R 472:472 /opt/nomad/scratch/grafana-data && echo OK"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "grafana" {
      driver = "containerd-driver"

      config {
        image = var.grafana_image
      }

      volume_mount {
        volume      = "scratch"
        destination = "/scratch"
        read_only   = false
      }

      env {
        GF_SERVER_HTTP_PORT   = "3000"
        GF_PATHS_PROVISIONING = "/local/provisioning"
        # Persist Grafana data (SQLite DB, sessions, plugins) to scratch volume
        GF_PATHS_DATA         = "/scratch/grafana-data"
        GF_PATHS_LOGS         = "/scratch/grafana-data/logs"
        GF_PATHS_PLUGINS      = "/scratch/grafana-data/plugins"
        # Allow provisioned dashboards to be edited in the UI (lab convenience)
        GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH = "/local/provisioning/dashboards/files/nomad-loki-logs.json"
      }

      # Admin password: Nomad Variable takes precedence, HCL variable is fallback.
      template {
        destination = "secrets/grafana.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-grafana" -}}
GF_SECURITY_ADMIN_PASSWORD={{ .admin_password }}
{{- else -}}
GF_SECURITY_ADMIN_PASSWORD=${var.grafana_admin_password}
{{- end }}
EOF
      }

      # ── Datasources ──────────────────────────────────────────────────────────
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

      # ── Dashboard provider ───────────────────────────────────────────────────
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

      # ── Dashboard: Nomad allocation logs ────────────────────────────────────
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

      # ── Dashboard: Pipeline Jobs Monitor ────────────────────────────────────
      # Uses labels that are currently present in Alloy->Loki streams on abc-nodes:
      # alloc_id, filename, stream, task, service_name.
      template {
        destination = "local/provisioning/dashboards/files/pipeline-monitor.json"
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
      "gridPos": { "h": 10, "w": 24, "x": 0, "y": 0 },
      "id": 1,
      "options": {
        "dedupStrategy": "none",
        "enableLogDetails": true,
        "showLabels": true,
        "showTime": true,
        "sortOrder": "Descending",
        "wrapLogMessage": true
      },
      "targets": [
        {
          "datasource": { "type": "loki", "uid": "loki" },
          "editorMode": "code",
          "expr": "{task=~\"main|test\", stream=~\"stdout|stderr\"}",
          "queryType": "range",
          "refId": "A"
        }
      ],
      "title": "Recent Workload Logs (main/test tasks)",
      "type": "logs"
    },
    {
      "datasource": { "type": "loki", "uid": "loki" },
      "gridPos": { "h": 10, "w": 24, "x": 0, "y": 10 },
      "id": 2,
      "options": {
        "dedupStrategy": "none",
        "enableLogDetails": true,
        "showLabels": true,
        "showTime": true,
        "sortOrder": "Descending",
        "wrapLogMessage": true
      },
      "targets": [
        {
          "datasource": { "type": "loki", "uid": "loki" },
          "editorMode": "code",
          "expr": "{task=~\"notifier|traefik|tusd|grafana|loki|prometheus|minio\", stream=~\"stdout|stderr\"}",
          "queryType": "range",
          "refId": "A"
        }
      ],
      "title": "Core Service Logs (selected tasks)",
      "type": "logs"
    },
    {
      "datasource": { "type": "loki", "uid": "loki" },
      "gridPos": { "h": 8, "w": 24, "x": 0, "y": 20 },
      "id": 3,
      "options": {
        "dedupStrategy": "none",
        "enableLogDetails": true,
        "showLabels": false,
        "showTime": true,
        "sortOrder": "Descending",
        "wrapLogMessage": false
      },
      "targets": [
        {
          "datasource": { "type": "loki", "uid": "loki" },
          "editorMode": "code",
          "expr": "{task=\"notifier\", stream=~\"stdout|stderr\"} |~ \"sent:\"",
          "queryType": "range",
          "refId": "A"
        }
      ],
      "title": "Job Status Notifications (sent by job-notifier → ntfy)",
      "type": "logs"
    }
  ],
  "refresh": "30s",
  "schemaVersion": 39,
  "tags": ["abc-nodes", "pipeline", "nextflow", "bioinformatics"],
  "templating": { "list": [] },
  "time": { "from": "now-6h", "to": "now" },
  "timepicker": {},
  "timezone": "",
  "title": "Pipeline Jobs Monitor",
  "uid": "abc-nodes-pipeline-monitor",
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
