# ═══════════════════════════════════════════════════════════════════════════
# abc-nodes Enhanced + Experimental Services — Terraform Managed Deployment
# ═══════════════════════════════════════════════════════════════════════════
#
# TIERS
# ─────
#   Basic tier      minio · auth · tusd · uppy
#                   Owned by the abc CLI (nomad job run / nomad-pack).
#                   NOT managed here — assume already running.
#
#   Enhanced tier   Everything else in abc-services / abc-applications.
#                   Each service has an enable_<name> variable (default true).
#
#   Experimental    In-progress / evaluation services in abc-experimental.
#                   All default to false — explicit opt-in required.
#
# NAMESPACES
# ──────────
#   abc-services      enhanced platform services
#   abc-applications  enhanced user-facing apps
#   abc-experimental  experimental / WIP services
#   abc-automations   automation functions (fx hooks, event bridges)
#
# DEPLOYMENT ORDER
# ────────────────
#   0. Namespaces
#   1. Networking    (traefik)
#   2. Storage       (rustfs)
#   3. Observability (prometheus → loki → grafana, alloy)
#   4. Notifications (ntfy → job-notifier)
#   5. System        (boundary-worker)
#   6. Optional      (docker-registry)
#   7. Experimental  (postgres, redis → wave, supabase; restic-server; xtdb)
#   8. Automations   (fx-notify, fx-tusd-hook)  ← abc-automations namespace
#
# IMPORT HINTS
# ────────────
#   terraform import nomad_namespace.abc_experimental abc-experimental
#   terraform import nomad_namespace.abc_automations  abc-automations
#   terraform import nomad_job.traefik                "abc-nodes-traefik@abc-services"
#   terraform import nomad_job.xtdb                   "abc-experimental-xtdb@abc-experimental"
#   terraform import nomad_job.fx_notify              "fx-notify@abc-automations"
#   terraform import nomad_job.fx_tusd_hook           "fx-tusd-hook@abc-automations"
#
# ═══════════════════════════════════════════════════════════════════════════

locals {
  # Resolve per-service flags, honouring deprecated shim variables as
  # fallbacks when the per-service flag has not been explicitly set.
  # The ternary reads: "if the new flag equals its default AND the old shim
  # was explicitly disabled, honour the shim" — this way existing tfvars
  # that set deploy_observability_stack=false still work.
  obs_enabled      = var.enable_prometheus || var.enable_loki || var.enable_grafana || var.enable_alloy || var.deploy_observability_stack
  prometheus_count = (var.enable_prometheus && var.deploy_observability_stack) ? 1 : 0
  loki_count       = (var.enable_loki && var.deploy_observability_stack) ? 1 : 0
  grafana_count    = (var.enable_grafana && var.deploy_observability_stack) ? 1 : 0
  alloy_count      = (var.enable_alloy && var.deploy_observability_stack) ? 1 : 0
  boundary_count   = (var.enable_boundary_worker && var.deploy_boundary_worker) ? 1 : 0
  registry_count   = (var.enable_docker_registry || var.deploy_optional_services) ? 1 : 0

  # Caddy: resolve public host and Tailscale IP, falling back to cluster-wide defaults.
  caddy_public_host  = var.caddy_cluster_public_host != "" ? var.caddy_cluster_public_host : var.cluster_public_host
  caddy_tailscale_ip = var.caddy_tailscale_ip != "" ? var.caddy_tailscale_ip : var.cluster_tailscale_ip
}

# ───────────────────────────────────────────────────────────────────────────
# Namespaces — must exist before any job is registered
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_namespace" "abc_services" {
  name        = "abc-services"
  description = "abc-nodes enhanced-tier platform services (traefik, observability, …)"
}

resource "nomad_namespace" "abc_applications" {
  name        = "abc-applications"
  description = "abc-nodes enhanced-tier user-facing applications"
}

resource "nomad_namespace" "abc_experimental" {
  name        = "abc-experimental"
  description = "WIP / evaluation services — not production-ready; may be torn down at any time"
}

resource "nomad_namespace" "abc_automations" {
  name        = "abc-automations"
  description = "Automation functions — fx hooks, event bridges, and lightweight glue services"
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Networking
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "traefik" {
  count = var.enable_traefik ? 1 : 0

  jobspec = file("${path.module}/../nomad/traefik.nomad.hcl")
  detach  = false

  depends_on = [nomad_namespace.abc_services]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Storage
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "rustfs" {
  count = var.enable_rustfs ? 1 : 0

  jobspec = file("${path.module}/../nomad/rustfs.nomad.hcl")
  detach  = false

  depends_on = [nomad_namespace.abc_services]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Observability
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "prometheus" {
  count = local.prometheus_count

  jobspec = file("${path.module}/../nomad/prometheus.nomad.hcl")
  detach  = false

  depends_on = [nomad_namespace.abc_services]
}

resource "nomad_job" "loki" {
  count = local.loki_count

  jobspec = file("${path.module}/../nomad/loki.nomad.hcl")
  detach  = false

  # Loki stores logs in MinIO — basic-tier minio assumed present.
  depends_on = [nomad_namespace.abc_services]
}

resource "nomad_job" "grafana" {
  count = local.grafana_count

  # grafana.nomad.hcl embeds dashboard JSON via file(abspath(...)) which the
  # Nomad provider's HCL2 parser disables.  templatefile() pre-processes the
  # file in Terraform's own evaluation context.  ${...} in the JSON that
  # Grafana uses for template variables must be escaped to $${...} so the
  # Nomad provider's HCL2 parser treats them as literals.
  # format() constructs the escape sequences because Terraform's template
  # engine cannot express the literal sequence $${ in a quoted string.
  jobspec = templatefile("${path.module}/../nomad/grafana.nomad.hcl.tftpl", {
    dashboard_abc_nodes = replace(
      file("${path.module}/../nomad/grafana-dashboard-abc-nodes.json"),
      format("%s{", "$"), format("%s%s{", "$", "$")
    )
    dashboard_usage_overview = replace(
      file("${path.module}/../nomad/grafana-dashboard-usage-overview.json"),
      format("%s{", "$"), format("%s%s{", "$", "$")
    )
    dashboard_bucket_usage = replace(
      file("${path.module}/../nomad/grafana-dashboard-bucket-usage.json"),
      format("%s{", "$"), format("%s%s{", "$", "$")
    )
  })
  detach = false

  depends_on = [
    nomad_job.prometheus,
    nomad_job.loki,
  ]
}

resource "nomad_job" "alloy" {
  count = local.alloy_count

  jobspec = file("${path.module}/../nomad/alloy.nomad.hcl")
  detach  = false

  depends_on = [
    nomad_job.prometheus,
    nomad_job.loki,
  ]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Notifications
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "ntfy" {
  count = var.enable_ntfy ? 1 : 0

  jobspec = file("${path.module}/../nomad/ntfy.nomad.hcl")
  detach  = false

  depends_on = [nomad_namespace.abc_services]
}

resource "nomad_job" "job_notifier" {
  count = var.enable_job_notifier ? 1 : 0

  # Nomad workload-identity JWT verification returns 500 on this cluster, so
  # the nomadVar template function can't read secrets at runtime.  The token
  # is injected directly via templatefile() at deploy time instead.
  jobspec = templatefile("${path.module}/../nomad/job-notifier.nomad.hcl.tftpl", {
    nomad_token = var.nomad_token != "" ? var.nomad_token : "6acff123-f6eb-70c6-48d6-9650fdf2c45f"
  })
  detach = false

  depends_on = [nomad_job.ntfy]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — System Jobs
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "boundary_worker" {
  count = local.boundary_count

  jobspec = file("${path.module}/../nomad/boundary-worker.nomad.hcl")
  detach  = false

  # Deploy last — platform should be stable before workers register.
  # auth is basic-tier (assumed present); traefik is enhanced-tier.
  depends_on = [nomad_job.traefik]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Optional
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "docker_registry" {
  count = local.registry_count

  jobspec = file("${path.module}/../nomad/docker-registry.nomad.hcl")
  detach  = false

  depends_on = [nomad_namespace.abc_services]
}

# ───────────────────────────────────────────────────────────────────────────
# Experimental Tier — Infrastructure dependencies
# All jobs in this section run in the abc-experimental namespace.
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "postgres" {
  count = var.enable_postgres ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/postgres.nomad.hcl.tftpl", {
    postgres_image    = var.postgres_image
    postgres_db       = var.postgres_db
    postgres_user     = var.postgres_user
    postgres_password = var.postgres_password
  })
  detach = false

  depends_on = [nomad_namespace.abc_experimental]
}

resource "nomad_job" "redis" {
  count = var.enable_redis ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/redis.nomad.hcl.tftpl", {
    redis_image = var.redis_image
  })
  detach = false

  depends_on = [nomad_namespace.abc_experimental]
}

# ───────────────────────────────────────────────────────────────────────────
# Experimental Tier — Application services
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "wave" {
  count = var.enable_wave ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/wave.nomad.hcl.tftpl", {
    wave_image        = var.wave_image
    postgres_user     = var.postgres_user
    postgres_password = var.postgres_password
    postgres_db       = var.postgres_db
  })
  detach = false

  # Wave requires postgres (job metadata) and redis (rate-limit / queue).
  depends_on = [
    nomad_job.postgres,
    nomad_job.redis,
  ]
}

resource "nomad_job" "supabase" {
  count = var.enable_supabase ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/supabase.nomad.hcl.tftpl", {
    supabase_image    = var.supabase_image
    postgres_password = var.postgres_password
  })
  detach = false

  # Supabase uses the shared postgres instance.
  depends_on = [nomad_job.postgres]
}

resource "nomad_job" "restic_server" {
  count = var.enable_restic_server ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/restic-server.nomad.hcl.tftpl", {
    restic_server_image    = var.restic_server_image
    restic_server_htpasswd = var.restic_server_htpasswd
  })
  detach = false

  # Standalone — no shared service deps beyond the namespace.
  depends_on = [nomad_namespace.abc_experimental]
}

resource "nomad_job" "caddy" {
  count = var.enable_caddy ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/caddy.nomad.hcl.tftpl", {
    caddy_image        = var.caddy_image
    caddy_public_host  = local.caddy_public_host
    caddy_tailscale_ip = local.caddy_tailscale_ip
    traefik_addr       = var.enable_traefik ? "${var.cluster_tailscale_ip}:80" : ""
    nomad_addr         = var.nomad_address
  })
  detach = false

  # Caddy may proxy to Traefik; deploy after networking is up.
  depends_on = [
    nomad_namespace.abc_experimental,
    nomad_job.traefik,
  ]
}

resource "nomad_job" "xtdb" {
  count = var.enable_xtdb ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/xtdb-v2.nomad.hcl.tftpl", {
    xtdb_image        = var.xtdb_image
    xtdb_node         = var.xtdb_node
    xtdb_http_port    = var.xtdb_http_port
    xtdb_postgres_url = var.xtdb_postgres_url
  })
  detach = false

  depends_on = [
    nomad_namespace.abc_experimental,
    # When a Postgres txLog URL is provided, ensure postgres is running first.
    # This is a soft dependency (no count-conditional syntax) — if postgres is
    # not managed by Terraform, remove this line and bring it up manually.
    nomad_job.postgres,
  ]
}

# ───────────────────────────────────────────────────────────────────────────
# Automations Tier — fx event-driven microservices
# All jobs in this section run in the abc-automations namespace.
# Both are lightweight Python HTTP servers; no Docker, no compiled binaries.
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "fx_notify" {
  count = var.enable_fx_notify ? 1 : 0

  jobspec = file("${path.module}/../nomad/fx/fx-notify.nomad.hcl")
  detach  = false

  # Pass Terraform variables through to the Nomad HCL2 variable blocks so
  # the node constraint and ntfy URL are controlled from this Terraform config
  # rather than relying on in-file defaults.
  hcl2 {
    vars = {
      docker_node = var.fx_notify_node
      ntfy_url    = var.fx_notify_ntfy_url
    }
  }

  depends_on = [
    nomad_namespace.abc_automations,
    # ntfy must be running before fx-notify can forward events.
    nomad_job.ntfy,
  ]
}

resource "nomad_job" "fx_tusd_hook" {
  count = var.enable_fx_tusd_hook ? 1 : 0

  jobspec = file("${path.module}/../nomad/fx/fx-tusd-hook.nomad.hcl")
  detach  = false

  # All tusd hook parameters are surfaced as Terraform variables so MinIO
  # credentials and node placement can be overridden without editing the HCL.
  hcl2 {
    vars = {
      docker_node    = var.fx_tusd_hook_node
      ntfy_url       = var.fx_tusd_hook_ntfy_url
      minio_endpoint = var.fx_tusd_hook_minio_endpoint
      minio_bucket   = var.fx_tusd_hook_minio_bucket
      s3_access_key  = var.fx_tusd_hook_s3_access_key
      s3_secret_key  = var.fx_tusd_hook_s3_secret_key
    }
  }

  depends_on = [
    nomad_namespace.abc_automations,
    # ntfy must be running before the hook can deliver upload notifications.
    nomad_job.ntfy,
  ]
}
