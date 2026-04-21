# Supabase — PostgreSQL platform with REST API, Auth, Studio UI, and API gateway
# Replaces the standalone postgres.nomad.hcl
#
# ┌─ ARCHITECTURE (single-node, host network) ──────────────────────────────────┐
# │  db      supabase/postgres:15.8.1.060      :5432   PostgreSQL + extensions  │
# │  rest    postgrest/postgrest:v12.2.8        :3001   REST API over PostgreSQL │
# │  auth    supabase/gotrue:v2.170.0           :9999   Auth (GoTrue)            │
# │  meta    supabase/postgres-meta:v0.84.2     :8081   Schema introspection     │
# │  studio  supabase/studio:20250317-6955350   :3002   Supabase Studio web UI   │
# │  kong    kong:2.8.1                         :8000   API gateway              │
# └──────────────────────────────────────────────────────────────────────────────┘
#
# IMPORTANT: Each service is its own task group. All groups use mode="bridge"
# with static port mappings — Nomad/CNI creates iptables rules that forward
# host ports to container ports. This is how containerd-driver exposes ports
# on this cluster (mode="host" does NOT work with containerd-driver here).
#
# DATA PERSISTENCE
# ─────────────────
#  PostgreSQL data → /opt/nomad/scratch/supabase-data (bind-mounted to /var/lib/postgresql/data)
#
# CREDENTIALS (Nomad Variables, namespace: services)
# ───────────────────────────────────────────────────
#  Path: nomad/jobs/abc-nodes-supabase
#  Keys: postgres_password, jwt_secret, anon_key, service_role_key, wave_db_password
#
#  Generate + store: bash deployments/abc-nodes/scripts/init-supabase-secrets.sh
#
# TRAEFIK
# ───────
#  supabase.aither        → Kong :8000
#  supabase-studio.aither → Studio :3002
#
# DEPLOY
# ──────
#  1. bash deployments/abc-nodes/scripts/init-supabase-secrets.sh
#  2. abc admin services nomad cli -- job run deployments/abc-nodes/nomad/supabase.nomad.hcl

job "abc-nodes-supabase" {
  namespace = "services"
  type      = "service"
  priority  = 85

  # ── DB ─────────────────────────────────────────────────────────────────────
  group "db" {
    count = 1

    network {
      mode = "bridge"
      port "db" {
        static = 5432
        to     = 5432
      }
    }

    restart {
      attempts = 5
      delay    = "30s"
      interval = "5m"
      mode     = "delay"
    }

    # Prestart: write the Wave init SQL to a fixed host path.
    # supabase/postgres runs /docker-entrypoint-initdb.d/*.sql on first init.
    # Uses a raw_exec prestart so nomadVar is available at runtime (not job-submit time).
    task "db-prep" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
      driver = "raw_exec"
      config {
        command = "/bin/sh"
        args    = ["-c", "local/db-prep.sh"]
      }
      template {
        destination = "local/db-prep.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
set -e
mkdir -p /opt/nomad/scratch/supabase-init
chmod 755 /opt/nomad/scratch/supabase-init

{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
WAVE_PW='{{ .wave_db_password }}'
{{- else -}}
WAVE_PW='wave_db_secret'
{{- end }}

cat > /opt/nomad/scratch/supabase-init/99-wave.sql <<SQL
DO \$\$ BEGIN
  CREATE ROLE wave LOGIN PASSWORD '$WAVE_PW';
EXCEPTION WHEN duplicate_object THEN
  ALTER ROLE wave WITH PASSWORD '$WAVE_PW';
END \$\$;
SELECT 'CREATE DATABASE wave OWNER wave'
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'wave')\gexec
GRANT ALL PRIVILEGES ON DATABASE wave TO wave;
SQL
chmod 644 /opt/nomad/scratch/supabase-init/99-wave.sql
echo "==> init SQL ready"
EOF
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "db" {
      driver = "containerd-driver"

      config {
        image = "supabase/postgres:15.8.1.060"
        mounts = [
          {
            type    = "bind"
            source  = "/opt/nomad/scratch/supabase-data"
            target  = "/var/lib/postgresql/data"
            options = ["rbind"]
          },
          {
            type    = "bind"
            source  = "/opt/nomad/scratch/supabase-init"
            target  = "/docker-entrypoint-initdb.d"
            options = ["rbind", "ro"]
          }
        ]
      }

      template {
        destination = "secrets/db.env"
        env         = true
        data        = <<EOF
{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
POSTGRES_PASSWORD={{ .postgres_password }}
{{- else -}}
POSTGRES_PASSWORD=supabase-postgres
{{- end }}
POSTGRES_HOST=/var/run/postgresql
EOF
      }

      resources {
        cpu    = 600
        memory = 1024
      }

      service {
        name     = "abc-nodes-supabase-db"
        port     = "db"
        provider = "nomad"
        tags     = ["abc-nodes", "supabase", "postgres", "db"]

        check {
          type     = "tcp"
          interval = "15s"
          timeout  = "5s"
        }
      }
    }
  }

  # ── REST (PostgREST) ────────────────────────────────────────────────────────
  group "rest" {
    count = 1

    network {
      mode = "bridge"
      port "rest" {
        static = 3001
        to     = 3001
      }
    }

    restart {
      attempts = 10
      delay    = "15s"
      interval = "5m"
      mode     = "delay"
    }

    task "rest" {
      driver = "containerd-driver"

      config {
        image = "postgrest/postgrest:v12.2.8"
      }

      template {
        destination = "secrets/rest.env"
        env         = true
        data        = <<EOF
PGRST_SERVER_PORT=3001
PGRST_DB_SCHEMAS=public,storage,graphql_public
PGRST_DB_ANON_ROLE=anon
PGRST_DB_USE_LEGACY_GUCS=false
PGRST_APP_SETTINGS_JWT_EXP=3600
{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
PGRST_DB_URI=postgresql://authenticator:{{ .postgres_password }}@100.70.185.46:5432/postgres
PGRST_JWT_SECRET={{ .jwt_secret }}
PGRST_APP_SETTINGS_JWT_SECRET={{ .jwt_secret }}
{{- else -}}
PGRST_DB_URI=postgresql://authenticator:supabase-postgres@100.70.185.46:5432/postgres
PGRST_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long
PGRST_APP_SETTINGS_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long
{{- end }}
EOF
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }

  # ── AUTH (GoTrue) ───────────────────────────────────────────────────────────
  group "auth" {
    count = 1

    network {
      mode = "bridge"
      port "auth" {
        static = 9999
        to     = 9999
      }
    }

    restart {
      attempts = 10
      delay    = "15s"
      interval = "5m"
      mode     = "delay"
    }

    task "auth" {
      driver = "containerd-driver"

      config {
        image = "supabase/gotrue:v2.170.0"
      }

      template {
        destination = "secrets/auth.env"
        env         = true
        data        = <<EOF
GOTRUE_API_HOST=0.0.0.0
GOTRUE_API_PORT=9999
GOTRUE_DB_DRIVER=postgres
GOTRUE_SITE_URL=http://100.70.185.46:8000
GOTRUE_URI_ALLOW_LIST=
GOTRUE_DISABLE_SIGNUP=false
GOTRUE_JWT_ADMIN_ROLES=service_role
GOTRUE_JWT_AUD=authenticated
GOTRUE_JWT_DEFAULT_GROUP_NAME=authenticated
GOTRUE_JWT_EXP=3600
GOTRUE_EXTERNAL_EMAIL_ENABLED=true
GOTRUE_MAILER_AUTOCONFIRM=true
GOTRUE_SMTP_ADMIN_EMAIL=admin@example.com
GOTRUE_SMTP_HOST=100.70.185.46
GOTRUE_SMTP_PORT=25
API_EXTERNAL_URL=http://100.70.185.46:8000
{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
GOTRUE_DB_DATABASE_URL=postgresql://supabase_auth_admin:{{ .postgres_password }}@100.70.185.46:5432/postgres
GOTRUE_JWT_SECRET={{ .jwt_secret }}
{{- else -}}
GOTRUE_DB_DATABASE_URL=postgresql://supabase_auth_admin:supabase-postgres@100.70.185.46:5432/postgres
GOTRUE_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long
{{- end }}
EOF
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }

  # ── META (postgres-meta) ────────────────────────────────────────────────────
  group "meta" {
    count = 1

    network {
      mode = "bridge"
      port "meta" {
        static = 8081
        to     = 8081
      }
    }

    restart {
      attempts = 10
      delay    = "15s"
      interval = "5m"
      mode     = "delay"
    }

    task "meta" {
      driver = "containerd-driver"

      config {
        image = "supabase/postgres-meta:v0.84.2"
      }

      template {
        destination = "secrets/meta.env"
        env         = true
        data        = <<EOF
PG_META_PORT=8081
PG_META_DB_HOST=100.70.185.46
PG_META_DB_PORT=5432
PG_META_DB_NAME=postgres
PG_META_DB_USER=supabase_admin
{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
PG_META_DB_PASSWORD={{ .postgres_password }}
{{- else -}}
PG_META_DB_PASSWORD=supabase-postgres
{{- end }}
EOF
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }

  # ── STUDIO ──────────────────────────────────────────────────────────────────
  group "studio" {
    count = 1

    network {
      mode = "bridge"
      port "studio" {
        static = 3002
        to     = 3000
      }
    }

    restart {
      attempts = 5
      delay    = "20s"
      interval = "5m"
      mode     = "delay"
    }

    task "studio" {
      driver = "containerd-driver"

      config {
        image = "supabase/studio:20250317-6955350"
      }

      template {
        destination = "secrets/studio.env"
        env         = true
        data        = <<EOF
STUDIO_PG_META_URL=http://100.70.185.46:8081
DEFAULT_ORGANIZATION_NAME=abc-nodes
DEFAULT_PROJECT_NAME=aither
SUPABASE_URL=http://100.70.185.46:8000
SUPABASE_PUBLIC_URL=http://100.70.185.46:8000
NEXT_PUBLIC_ENABLE_LOGS=false
NEXT_ANALYTICS_BACKEND_PROVIDER=postgres
LOGFLARE_API_URL=http://100.70.185.46:4000
{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
POSTGRES_PASSWORD={{ .postgres_password }}
SUPABASE_ANON_KEY={{ .anon_key }}
SUPABASE_SERVICE_KEY={{ .service_role_key }}
{{- else -}}
POSTGRES_PASSWORD=supabase-postgres
SUPABASE_ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24iLCJleHAiOjE5ODM4MTI5OTZ9.CRFA0NiK7ACcPzu8kVTNM2DXZiXJKkwzCDNmxHmE-Co
SUPABASE_SERVICE_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSIsImV4cCI6MTk4MzgxMjk5Nn0.EGIM96RAZx35lJzdJsyH-qQwv8Hj04zWl196z2-SBc0
{{- end }}
EOF
      }

      resources {
        cpu    = 500
        memory = 768
      }

      service {
        name     = "abc-nodes-supabase-studio"
        port     = "studio"
        provider = "nomad"
        tags     = ["abc-nodes", "supabase", "studio", "ui"]

        check {
          type     = "http"
          path     = "/api/profile"
          interval = "30s"
          timeout  = "10s"
        }
      }
    }
  }

  # ── KONG (API gateway) ──────────────────────────────────────────────────────
  group "kong" {
    count = 1

    network {
      mode = "bridge"
      port "kong" {
        static = 8000
        to     = 8000
      }
    }

    restart {
      attempts = 10
      delay    = "15s"
      interval = "5m"
      mode     = "delay"
    }

    # Prestart: write Kong declarative config with runtime JWT keys from nomadVar.
    task "kong-prep" {
      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
      driver = "raw_exec"
      config {
        command = "/bin/sh"
        args    = ["-c", "local/kong-prep.sh"]
      }
      template {
        destination = "local/kong-prep.sh"
        perms       = "755"
        data        = <<EOF
#!/bin/sh
set -e
mkdir -p /opt/nomad/scratch/supabase-init

{{ with nomadVar "nomad/jobs/abc-nodes-supabase" -}}
ANON_KEY='{{ .anon_key }}'
SVC_KEY='{{ .service_role_key }}'
{{- else -}}
ANON_KEY='eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24iLCJleHAiOjE5ODM4MTI5OTZ9.CRFA0NiK7ACcPzu8kVTNM2DXZiXJKkwzCDNmxHmE-Co'
SVC_KEY='eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSIsImV4cCI6MTk4MzgxMjk5Nn0.EGIM96RAZx35lJzdJsyH-qQwv8Hj04zWl196z2-SBc0'
{{- end }}

cat > /opt/nomad/scratch/supabase-init/kong.yml <<KONG
_format_version: "1.1"
services:
  - name: auth-v1
    url: http://100.70.185.46:9999/
    routes:
      - name: auth-v1-route
        strip_path: true
        paths:
          - /auth/v1/
    plugins:
      - name: cors

  - name: rest-v1
    url: http://100.70.185.46:3001/
    routes:
      - name: rest-v1-route
        strip_path: true
        paths:
          - /rest/v1/
    plugins:
      - name: cors
      - name: key-auth
        config:
          hide_credentials: false
      - name: acl
        config:
          hide_groups_header: true
          allow:
            - anon
            - service_role

  - name: meta-v1
    url: http://100.70.185.46:8081/
    routes:
      - name: meta-v1-route
        strip_path: true
        paths:
          - /pg/

  - name: studio
    url: http://100.70.185.46:3002/
    routes:
      - name: studio-route
        paths:
          - /

consumers:
  - username: anon
    keyauth_credentials:
      - key: $ANON_KEY
    acls:
      - group: anon
  - username: service_role
    keyauth_credentials:
      - key: $SVC_KEY
    acls:
      - group: service_role
KONG
chmod 644 /opt/nomad/scratch/supabase-init/kong.yml
echo "==> kong.yml ready"
EOF
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "kong" {
      driver = "containerd-driver"

      config {
        image = "kong:2.8.1"
        mounts = [
          {
            type    = "bind"
            source  = "/opt/nomad/scratch/supabase-init/kong.yml"
            target  = "/home/kong/kong.yml"
            options = ["rbind", "ro"]
          }
        ]
      }

      env {
        KONG_DATABASE           = "off"
        KONG_DECLARATIVE_CONFIG = "/home/kong/kong.yml"
        KONG_DNS_ORDER          = "LAST,A,CNAME"
        KONG_PLUGINS            = "request-transformer,cors,key-auth,acl"
        KONG_NGINX_PROXY_PROXY_BUFFER_SIZE  = "160k"
        KONG_NGINX_PROXY_PROXY_BUFFERS      = "64 160k"
        KONG_HTTP_LISTEN        = "0.0.0.0:8000"
        KONG_HTTPS_LISTEN       = "0.0.0.0:8443"
      }

      resources {
        cpu    = 300
        memory = 384
      }

      service {
        name     = "abc-nodes-supabase"
        port     = "kong"
        provider = "nomad"
        tags     = ["abc-nodes", "supabase", "kong", "api"]

        check {
          type     = "http"
          path     = "/"
          interval = "30s"
          timeout  = "10s"
        }
      }
    }
  }
}
