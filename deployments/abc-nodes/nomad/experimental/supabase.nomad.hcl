# Supabase — BaaS platform (abc-experimental namespace)
# WIP: deploys Supabase Studio UI backed by the shared abc-experimental postgres instance.
# Full Supabase stack (auth, realtime, storage) may be added incrementally.
#
# Dependencies: postgres must be running in abc-experimental.
#
# Enable via Terraform:
#   terraform apply -var enable_postgres=true -var enable_supabase=true
#
# Traefik route: http://supabase.aither  (requires Traefik with host-based routing)

job "abc-experimental-supabase" {
  namespace = "abc-experimental"
  type      = "service"
  priority  = 50

  group "supabase" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "bridge"
      port "studio" {
        static = 3001
        to     = 3000
      }
    }

    restart {
      attempts = 3
      delay    = "30s"
      interval = "5m"
      mode     = "delay"
    }

    # ── Supabase Studio UI ───────────────────────────────────────────────────
    task "studio" {
      driver = "containerd-driver"

      config {
        image = "supabase/studio:latest"
      }

      env {
        # Studio reads the Postgres connection to display schema explorer.
        # Uses the shared abc-experimental postgres instance.
        SUPABASE_URL            = "http://supabase.aither"
        STUDIO_PG_META_URL      = "http://127.0.0.1:8080"
        POSTGRES_PASSWORD       = "abc_db_secret"

        # Next.js / Studio runtime
        NEXT_PUBLIC_ENABLE_LOGS = "true"
        NODE_ENV                = "production"
      }

      resources {
        cpu    = 500
        memory = 768
      }

      # Optional: expose Studio on Traefik via host-header routing.
      # Uncomment once Traefik is confirmed routing *.aither internally.
      # service {
      #   name = "supabase-studio"
      #   port = "studio"
      #   tags = [
      #     "traefik.enable=true",
      #     "traefik.http.routers.supabase.rule=Host(`supabase.aither`)",
      #     "traefik.http.routers.supabase.entrypoints=web",
      #   ]
      # }
    }
  }
}
