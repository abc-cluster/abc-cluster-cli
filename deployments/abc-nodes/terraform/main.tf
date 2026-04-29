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

  # Pass image / credentials through to the Nomad HCL2 variable block so they
  # can be overridden from tfvars without editing the jobspec. RustFS data is
  # persisted on aither's "scratch" host volume (see rustfs.nomad.hcl).
  hcl2 {
    vars = {
      rustfs_image      = var.rustfs_image
      rustfs_access_key = var.rustfs_access_key
      rustfs_secret_key = var.rustfs_secret_key
    }
  }

  depends_on = [nomad_namespace.abc_services]
}

# ── Docs (Docusaurus static site) — served at http://docs.aither ───────────
# Content is pushed via `just docs-deploy` to /opt/nomad/scratch/abc-docs on
# aither.  The job mounts that path and serves it via a tiny Caddy file_server.
# Restart not needed for content updates — only when changing the embedded
# Caddyfile.

resource "nomad_job" "docs" {
  count = var.enable_docs ? 1 : 0

  jobspec = file("${path.module}/../nomad/abc-nodes-docs.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      caddy_image = var.docs_caddy_image
    }
  }

  depends_on = [nomad_namespace.abc_services]
}

# ── Garage — long-term archive + backup tier (zstd compression + dedup) ────
# Sits behind RustFS as a cold-data + backup target.  Bootstrap (layout +
# buckets + key import) runs as a poststart task inside the job — see
# garage.nomad.hcl for the full sequence.

resource "random_password" "garage_rpc_secret" {
  length  = 64
  upper   = false
  special = false
  # 64 hex chars = 32 bytes; Garage's rpc_secret expects exactly that.
  override_special = ""
}

resource "random_password" "garage_admin_token" {
  length  = 48
  special = false
}

resource "random_password" "garage_metrics_token" {
  length  = 48
  special = false
}

# Garage S3 access keys are imported via `garage key import` during bootstrap
# so terraform owns them.  AWS access-key IDs are constrained to ~20 alnum chars;
# secret keys are 40 alnum chars.
resource "random_password" "garage_restic_secret_key" {
  length  = 40
  special = false
}

resource "random_password" "garage_archive_secret_key" {
  length  = 40
  special = false
}

# Restic repository encryption key — losing this loses ALL backups.  Surface
# via terraform output so an operator can stash it in the team password
# manager.  Treat as sensitive everywhere.
resource "random_password" "restic_repo_password" {
  length  = 48
  special = false
}

resource "nomad_job" "garage" {
  count = var.enable_garage ? 1 : 0

  jobspec = file("${path.module}/../nomad/garage.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      garage_image              = var.garage_image
      garage_webui_image        = var.garage_webui_image
      garage_rpc_secret         = random_password.garage_rpc_secret.result
      garage_admin_token        = random_password.garage_admin_token.result
      garage_metrics_token      = random_password.garage_metrics_token.result
      garage_restic_access_key  = var.garage_restic_access_key
      garage_restic_secret_key  = random_password.garage_restic_secret_key.result
      garage_archive_access_key = var.garage_archive_access_key
      garage_archive_secret_key = random_password.garage_archive_secret_key.result
      garage_node_capacity      = var.garage_node_capacity
      garage_zone               = var.garage_zone
    }
  }

  depends_on = [nomad_namespace.abc_services]
}

# ── abc-backups — nightly restic-on-Garage of cluster state ────────────────
# Periodic batch.  Snapshots Consul / Vault / Nomad job specs, encrypts via
# restic, stores in the `cluster-backups` bucket on Garage.  Replaces the
# experimental restic-server.nomad.hcl (kept in repo for reference).

resource "nomad_job" "abc_backups" {
  count = var.enable_abc_backups ? 1 : 0

  jobspec = file("${path.module}/../nomad/abc-backups.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      garage_endpoint          = var.garage_internal_endpoint
      garage_restic_access_key = var.garage_restic_access_key
      garage_restic_secret_key = random_password.garage_restic_secret_key.result
      garage_backup_bucket     = var.garage_backup_bucket
      restic_password          = random_password.restic_repo_password.result
      consul_addr              = var.backups_consul_addr
      consul_token             = var.backups_consul_token
      vault_addr               = var.backups_vault_addr
      vault_token              = var.backups_vault_token
      nomad_addr               = var.backups_nomad_addr
      nomad_token              = var.nomad_token
      keep_daily               = var.backups_keep_daily
      keep_weekly              = var.backups_keep_weekly
      keep_monthly             = var.backups_keep_monthly
      schedule_cron            = var.backups_schedule_cron
      ntfy_url                 = var.backups_ntfy_url
    }
  }

  depends_on = [
    nomad_job.garage,
    nomad_job.ntfy,
  ]
}

# ───────────────────────────────────────────────────────────────────────────
# Enhanced Tier — Observability
# ───────────────────────────────────────────────────────────────────────────

resource "nomad_job" "prometheus" {
  count = local.prometheus_count

  jobspec = file("${path.module}/../nomad/prometheus.nomad.hcl")
  detach  = false

  # Pass datacenters through so adding a new DC in variables.tf propagates
  # without touching the jobspec.  Service discovery (consul_sd_configs)
  # picks up new nodes automatically once they register in Consul.
  hcl2 {
    vars = {
      datacenters = jsonencode(var.datacenters)
    }
  }

  depends_on = [nomad_namespace.abc_services]
}

resource "nomad_job" "loki" {
  count = local.loki_count

  jobspec = file("${path.module}/../nomad/loki.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      datacenters = jsonencode(var.datacenters)
    }
  }

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

  hcl2 {
    vars = {
      datacenters = jsonencode(var.datacenters)
    }
  }

  depends_on = [
    nomad_job.prometheus,
    nomad_job.loki,
  ]
}

resource "nomad_job" "alloy" {
  count = local.alloy_count

  jobspec = file("${path.module}/../nomad/alloy.nomad.hcl")
  detach  = false

  # Alloy is a `system` job (one per node) and reaches Prometheus / Loki via
  # Consul service discovery, so it just needs the datacenters list.
  hcl2 {
    vars = {
      datacenters = jsonencode(var.datacenters)
    }
  }

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
  # the nomadVar template function can't read secrets at runtime.  We inject
  # the token via hcl2.vars instead (cleaner than templatefile — no double
  # escaping for shell expansions inside the heredoc body).
  jobspec = file("${path.module}/../nomad/job-notifier.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      nomad_token = var.nomad_token != "" ? var.nomad_token : "6acff123-f6eb-70c6-48d6-9650fdf2c45f"
    }
  }

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

  # Local OCI registry (registry:2) for pushing locally-built images and
  # pulling them back into Nomad jobs.  Lives in `abc-experimental` so it
  # can be torn down without touching the production-tier services; the
  # data lives on aither's scratch host volume so it survives rescheduling.
  jobspec = templatefile("${path.module}/../nomad/experimental/docker-registry.nomad.hcl.tftpl", {
    registry_image = var.docker_registry_image
    registry_node  = var.docker_registry_node
    registry_port  = var.docker_registry_port
    registry_host  = var.cluster_tailscale_ip
  })
  detach = false

  depends_on = [nomad_namespace.abc_experimental]
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

  # The supabase stack ports the upstream docker-compose to a single Nomad
  # job with multiple tasks sharing one bridge network namespace. It runs
  # its OWN postgres (supabase/postgres) — independent of the standalone
  # abc-experimental-postgres job — because the supabase services require
  # extensions and migration scripts that vanilla postgres does not have.
  jobspec = templatefile("${path.module}/../nomad/experimental/supabase.nomad.hcl.tftpl", {
    supabase_node                 = var.supabase_node
    supabase_db_image             = var.supabase_db_image
    supabase_studio_image         = var.supabase_studio_image
    supabase_meta_image           = var.supabase_meta_image
    supabase_auth_image           = var.supabase_auth_image
    supabase_rest_image           = var.supabase_rest_image
    supabase_kong_image           = var.supabase_kong_image
    kong_http_port                = var.kong_http_port
    supabase_postgres_db          = var.supabase_postgres_db
    supabase_postgres_password    = var.supabase_postgres_password
    supabase_jwt_secret           = var.supabase_jwt_secret
    supabase_jwt_exp              = var.supabase_jwt_exp
    supabase_anon_key             = var.supabase_anon_key
    supabase_service_role_key     = var.supabase_service_role_key
    supabase_dashboard_username   = var.supabase_dashboard_username
    supabase_dashboard_password   = var.supabase_dashboard_password
    supabase_pg_meta_crypto_key   = var.supabase_pg_meta_crypto_key
    supabase_public_url           = var.supabase_public_url
    supabase_site_url             = var.supabase_site_url
    supabase_disable_signup       = var.supabase_disable_signup
    supabase_pgrst_schemas        = var.supabase_pgrst_schemas
    supabase_default_org_name             = var.supabase_default_org_name
    supabase_default_project_name         = var.supabase_default_project_name
    supabase_enable_optional_integrations = var.supabase_enable_optional_integrations
  })
  # JVM-like cold start: postgres + 5 other JVM-ish services in parallel
  # easily exceed the 5-min hardcoded provider timeout.
  detach = true

  depends_on = [nomad_namespace.abc_experimental]
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

# ── Caddy (unified Tailscale + LAN gateway) — abc-experimental ─────────────
# This is the ACTIVE production gateway: a single Caddy raw_exec job binding
# port 80 on BOTH the Tailscale IP and the institutional LAN IP, owning all
# *.aither vhosts AND the subpath routing for the LAN landing page surface.
# Supersedes the older nomad_job.caddy resource above (which only handled
# ACME / TLS in front of Traefik).  Default to enabled; disable if you're
# rolling back to two-Caddy split.
#
# Variables surfaced as hcl2.vars: service_domain (e.g. "aither"), lan_host
# (institutional FQDN), lan_ip (institutional v4), ts_ip (Tailscale v4).
# These flow into Caddy's bind directives AND the landing-page JS toggle.

resource "nomad_job" "caddy_tailscale" {
  count = var.enable_caddy_tailscale ? 1 : 0

  jobspec = file("${path.module}/../nomad/experimental/caddy-tailscale.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      service_domain = var.caddy_tailscale_service_domain
      lan_host       = var.caddy_tailscale_lan_host
      lan_ip         = var.caddy_tailscale_lan_ip
      ts_ip          = var.caddy_tailscale_ts_ip
    }
  }

  # Owns port 80 on the LAN IP — Traefik (port 80 on Tailscale via Consul-LB
  # service) and the routing chain Caddy → Traefik:8081 → service must be up.
  depends_on = [
    nomad_namespace.abc_experimental,
    nomad_job.traefik,
  ]
}

# ── GitRiver — self-hosted Git platform (abc-experimental) ─────────────────
# Single-job two-group deploy: dedicated Postgres + the GitRiver server.  Used
# to host private projects, distribute releases / artifacts, and serve as the
# remote for Nomad job prestart `git clone` tasks.  Setup wizard runs on first
# launch — operator must complete admin user creation via the web UI.

resource "random_password" "gitriver_db_password" {
  length  = 40
  special = false
}

resource "nomad_job" "gitriver" {
  count = var.enable_gitriver ? 1 : 0

  jobspec = file("${path.module}/../nomad/experimental/gitriver.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      gitriver_image       = var.gitriver_image
      gitriver_db_user     = var.gitriver_db_user
      gitriver_db_name     = var.gitriver_db_name
      gitriver_db_password = random_password.gitriver_db_password.result
      gitriver_base_url    = var.gitriver_base_url
      gitriver_ssh_host_port = var.gitriver_ssh_host_port
      postgres_static_port = var.gitriver_postgres_port
    }
  }

  depends_on = [
    nomad_namespace.abc_experimental,
    # gitriver.aither vhost is served by caddy_tailscale; deploying GitRiver
    # before Caddy is fine (the vhost is just unreachable until Caddy comes up)
    # but listing the dep makes apply order obvious.
    nomad_job.caddy_tailscale,
  ]
}

# ── NATS — messaging + JetStream (abc-experimental) ───────────────────────
# Single-node NATS server with JetStream persistence on aither's scratch
# volume.  Cluster-internal messaging bus + durable streams / KV.  No auth
# on initial deploy — operators add NKEYs via the [authorization] block in
# the rendered nats.conf when this graduates out of abc-experimental.

resource "nomad_job" "nats" {
  count = var.enable_nats ? 1 : 0

  jobspec = file("${path.module}/../nomad/experimental/nats.nomad.hcl")
  detach  = false

  hcl2 {
    vars = {
      nats_image                = var.nats_image
      nats_server_name          = var.nats_server_name
      nats_client_port          = var.nats_client_port
      nats_monitoring_port      = var.nats_monitoring_port
      nats_jetstream_max_memory = var.nats_jetstream_max_memory
      nats_jetstream_max_file   = var.nats_jetstream_max_file
    }
  }

  depends_on = [
    nomad_namespace.abc_experimental,
    # nats.aither vhost is served by caddy_tailscale.
    nomad_job.caddy_tailscale,
  ]
}

resource "nomad_job" "xtdb" {
  count = var.enable_xtdb ? 1 : 0

  jobspec = templatefile("${path.module}/../nomad/experimental/xtdb-v2.nomad.hcl.tftpl", {
    xtdb_image        = var.xtdb_image
    xtdb_node         = var.xtdb_node
    xtdb_healthz_port = var.xtdb_healthz_port
    xtdb_pgwire_port  = var.xtdb_pgwire_port
    xtdb_postgres_url = var.xtdb_postgres_url
  })
  # detach = true: the Nomad Terraform provider has a hardcoded 5-minute wait
  # for deployment_successful, but the XTDB JVM takes ~4-5 min to initialise.
  # Detaching avoids the spurious timeout; Consul health checks confirm liveness.
  detach = true

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

resource "nomad_job" "fx_archive" {
  count = var.enable_fx_archive ? 1 : 0

  jobspec = file("${path.module}/../nomad/fx/fx-archive.nomad.hcl")
  detach  = false

  # Periodic batch — copies aged objects from RustFS buckets into Garage's
  # `archive` bucket nightly.  Reads from RustFS using rustfs admin creds;
  # writes to Garage using the imported `archive-key`.
  hcl2 {
    vars = {
      docker_node               = var.fx_archive_node
      rustfs_endpoint           = var.fx_archive_rustfs_endpoint
      rustfs_access_key         = var.rustfs_access_key
      rustfs_secret_key         = var.rustfs_secret_key
      garage_endpoint           = var.garage_internal_endpoint
      garage_archive_access_key = var.garage_archive_access_key
      garage_archive_secret_key = random_password.garage_archive_secret_key.result
      garage_bucket             = var.fx_archive_dest_bucket
      source_buckets            = var.fx_archive_source_buckets
      archive_age_days          = var.fx_archive_age_days
      delete_after_copy         = var.fx_archive_delete_after_copy
      ntfy_url                  = var.fx_archive_ntfy_url
      schedule_cron             = var.fx_archive_schedule_cron
    }
  }

  depends_on = [
    nomad_namespace.abc_automations,
    nomad_job.garage,
    nomad_job.rustfs,
    nomad_job.ntfy,
  ]
}
