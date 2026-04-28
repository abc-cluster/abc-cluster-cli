# ═══════════════════════════════════════════════════════════════════════════
# Outputs
# ═══════════════════════════════════════════════════════════════════════════

# ── Basic tier (abc CLI owned — informational only) ────────────────────────

output "basic_tier_note" {
  description = "Basic-tier services are managed by the abc CLI, not Terraform"
  value       = "minio · auth · tusd · uppy  →  deploy with: abc admin services nomad cli -- job run ..."
}

# ── Enhanced tier ──────────────────────────────────────────────────────────

output "enhanced_services" {
  description = "Enhanced-tier services managed by Terraform with their Nomad job IDs"
  value = {
    traefik         = var.enable_traefik ? nomad_job.traefik[0].id : "disabled"
    rustfs          = var.enable_rustfs ? nomad_job.rustfs[0].id : "disabled"
    garage          = var.enable_garage ? nomad_job.garage[0].id : "disabled"
    abc_backups     = var.enable_abc_backups ? nomad_job.abc_backups[0].id : "disabled"
    docs            = var.enable_docs ? nomad_job.docs[0].id : "disabled"
    caddy_tailscale = var.enable_caddy_tailscale ? nomad_job.caddy_tailscale[0].id : "disabled"
    prometheus      = local.prometheus_count > 0 ? nomad_job.prometheus[0].id : "disabled"
    loki            = local.loki_count > 0 ? nomad_job.loki[0].id : "disabled"
    grafana         = local.grafana_count > 0 ? nomad_job.grafana[0].id : "disabled"
    alloy           = local.alloy_count > 0 ? nomad_job.alloy[0].id : "disabled"
    ntfy            = var.enable_ntfy ? nomad_job.ntfy[0].id : "disabled"
    job_notifier    = var.enable_job_notifier ? nomad_job.job_notifier[0].id : "disabled"
    boundary_worker = local.boundary_count > 0 ? nomad_job.boundary_worker[0].id : "disabled"
  }
}

# ── Public endpoint cheatsheet ─────────────────────────────────────────────
# Print a quick reference of what *.aither URLs land where after a successful
# apply, so operators don't have to guess at the routing.

output "public_endpoints" {
  description = "Tailscale-side URLs exposed by the unified Caddy gateway"
  value = var.enable_caddy_tailscale ? {
    landing       = "http://${var.caddy_tailscale_ts_ip}/  (also http://${var.caddy_tailscale_lan_host}/ on LAN)"
    grafana       = "http://grafana.${var.caddy_tailscale_service_domain}/"
    nomad         = "http://nomad.${var.caddy_tailscale_service_domain}/"
    consul        = "http://consul.${var.caddy_tailscale_service_domain}/"
    traefik       = "http://traefik.${var.caddy_tailscale_service_domain}/dashboard/"
    rustfs_s3     = "http://rustfs.${var.caddy_tailscale_service_domain}/"
    rustfs_console = "http://rustfs.${var.caddy_tailscale_service_domain}/rustfs/console/"
    garage_s3     = var.enable_garage ? "http://garage.${var.caddy_tailscale_service_domain}/" : "n/a"
    garage_webui  = var.enable_garage ? "http://garage-webui.${var.caddy_tailscale_service_domain}/" : "n/a"
    docs          = var.enable_docs ? "http://docs.${var.caddy_tailscale_service_domain}/" : "n/a"
    ntfy          = var.enable_ntfy ? "http://ntfy.${var.caddy_tailscale_service_domain}/" : "n/a"
    uppy          = "http://uppy.${var.caddy_tailscale_service_domain}/"
    tusd          = "http://tusd.${var.caddy_tailscale_service_domain}/"
    vault         = "http://vault.${var.caddy_tailscale_service_domain}/"
    gitriver_http = var.enable_gitriver ? "http://gitriver.${var.caddy_tailscale_service_domain}/" : "n/a"
    gitriver_ssh  = var.enable_gitriver ? "ssh://git@${var.caddy_tailscale_ts_ip}:${var.gitriver_ssh_host_port}/<org>/<repo>.git" : "n/a"
    nats_client   = var.enable_nats ? "nats://${var.caddy_tailscale_ts_ip}:${var.nats_client_port}" : "n/a"
    nats_monitor  = var.enable_nats ? "http://nats.${var.caddy_tailscale_service_domain}/" : "n/a"
  } : null
}

# ── Garage secrets (sensitive — surface via `terraform output -raw …`) ─────
# These are randomly generated on first apply and persisted in state.  The
# restic_repo_password is the most critical: losing it loses ALL backups.
# Operators should retrieve it after `terraform apply` and store it in the
# team password manager (1Password / Bitwarden / etc.) out-of-band.

output "garage_admin_token" {
  description = "Bearer token for the Garage admin API (port 3903) and garage-webui"
  value       = var.enable_garage ? random_password.garage_admin_token.result : ""
  sensitive   = true
}

output "garage_metrics_token" {
  description = "Bearer token for /metrics on the Garage admin port (Prometheus scrape)"
  value       = var.enable_garage ? random_password.garage_metrics_token.result : ""
  sensitive   = true
}

output "garage_restic_secret_key" {
  description = "Garage S3 secret key for the restic-on-Garage backup repo"
  value       = var.enable_garage ? random_password.garage_restic_secret_key.result : ""
  sensitive   = true
}

output "garage_archive_secret_key" {
  description = "Garage S3 secret key for the fx-archive tier-down job"
  value       = var.enable_garage ? random_password.garage_archive_secret_key.result : ""
  sensitive   = true
}

output "restic_repo_password" {
  description = "Restic encryption key for cluster-backups. STORE OUT-OF-BAND — losing it loses ALL backups."
  value       = var.enable_abc_backups ? random_password.restic_repo_password.result : ""
  sensitive   = true
}

# ── Experimental tier ──────────────────────────────────────────────────────

output "experimental_services" {
  description = "Experimental-tier services in abc-experimental (all disabled by default)"
  value = {
    postgres        = var.enable_postgres ? nomad_job.postgres[0].id : "disabled"
    redis           = var.enable_redis ? nomad_job.redis[0].id : "disabled"
    wave            = var.enable_wave ? nomad_job.wave[0].id : "disabled"
    supabase        = var.enable_supabase ? nomad_job.supabase[0].id : "disabled"
    restic_server   = var.enable_restic_server ? nomad_job.restic_server[0].id : "disabled"
    caddy           = var.enable_caddy ? nomad_job.caddy[0].id : "disabled"
    xtdb            = var.enable_xtdb ? nomad_job.xtdb[0].id : "disabled"
    gitriver        = var.enable_gitriver ? nomad_job.gitriver[0].id : "disabled"
    nats            = var.enable_nats ? nomad_job.nats[0].id : "disabled"
    docker_registry = local.registry_count > 0 ? nomad_job.docker_registry[0].id : "disabled"
  }
}

output "gitriver_db_password" {
  description = "GitRiver Postgres password (auto-generated; surface for ad-hoc psql / DB inspection)"
  value       = var.enable_gitriver ? random_password.gitriver_db_password.result : ""
  sensitive   = true
}

# ── Automations tier ───────────────────────────────────────────────────────

output "automations_services" {
  description = "Automations-tier services in abc-automations (fx hooks)"
  value = {
    fx_notify    = var.enable_fx_notify ? nomad_job.fx_notify[0].id : "disabled"
    fx_tusd_hook = var.enable_fx_tusd_hook ? nomad_job.fx_tusd_hook[0].id : "disabled"
  }
}

# ── Endpoints ──────────────────────────────────────────────────────────────

output "service_endpoints" {
  description = "Public service endpoints (enhanced tier)"
  value = {
    traefik_dashboard = var.enable_traefik ? "http://${var.cluster_public_host}:8888" : "disabled"
    grafana           = local.grafana_count > 0 ? "http://${var.cluster_public_host}:3000" : "disabled"
    prometheus        = local.prometheus_count > 0 ? "http://${var.cluster_public_host}:9090" : "disabled"
    loki              = local.loki_count > 0 ? "http://${var.cluster_public_host}:3100" : "disabled"
    ntfy              = var.enable_ntfy ? "http://${var.cluster_public_host}:8088" : "disabled"
    rustfs_console    = var.enable_rustfs ? "http://${var.cluster_public_host}:9901" : "disabled"
  }
}

output "experimental_endpoints" {
  description = "Endpoints for enabled experimental services"
  value = {
    postgres      = var.enable_postgres ? "${var.cluster_tailscale_ip}:5432" : "disabled"
    redis         = var.enable_redis ? "${var.cluster_tailscale_ip}:6379" : "disabled"
    restic_server = var.enable_restic_server ? "http://${var.cluster_public_host}:8000" : "disabled"
    supabase      = var.enable_supabase ? "http://${var.cluster_tailscale_ip}:${var.kong_http_port}  (vhost: ${var.supabase_public_url})  user=${var.supabase_dashboard_username}" : "disabled"
    caddy         = var.enable_caddy ? "http://${var.cluster_public_host}:2015  https://${var.cluster_public_host}:443" : "disabled"
    xtdb_healthz  = var.enable_xtdb ? "http://${var.cluster_tailscale_ip}:${var.xtdb_healthz_port}/healthz  (vhost: http://xtdb.aither/healthz)" : "disabled"
    xtdb_pgwire   = var.enable_xtdb ? "psql -h ${var.cluster_tailscale_ip} -p ${var.xtdb_pgwire_port} xtdb" : "disabled"
    docker_registry = local.registry_count > 0 ? "${var.cluster_tailscale_ip}:${var.docker_registry_port}  (push: docker push ${var.cluster_tailscale_ip}:${var.docker_registry_port}/<image>:<tag>;  vhost: http://registry.aither/v2/_catalog)" : "disabled"
  }
}

output "automations_endpoints" {
  description = "Traefik vhost endpoints for enabled automations-tier fx services"
  value = {
    fx_notify    = var.enable_fx_notify ? "http://notify.aither (port ${14001})" : "disabled"
    fx_tusd_hook = var.enable_fx_tusd_hook ? "http://tusd-hook.aither (port ${14002})" : "disabled"
  }
}

# ── Summary ────────────────────────────────────────────────────────────────

output "deployment_summary" {
  description = "Deployment configuration summary"
  value = {
    nomad_address = var.nomad_address
    nomad_region  = var.nomad_region

    namespaces = {
      enhanced     = "abc-services + abc-applications"
      experimental = "abc-experimental"
      automations  = "abc-automations"
    }

    enhanced_enabled = {
      traefik         = var.enable_traefik
      rustfs          = var.enable_rustfs
      prometheus      = local.prometheus_count > 0
      loki            = local.loki_count > 0
      grafana         = local.grafana_count > 0
      alloy           = local.alloy_count > 0
      ntfy            = var.enable_ntfy
      job_notifier    = var.enable_job_notifier
      boundary_worker = local.boundary_count > 0
    }

    experimental_enabled = {
      postgres        = var.enable_postgres
      redis           = var.enable_redis
      wave            = var.enable_wave
      supabase        = var.enable_supabase
      restic_server   = var.enable_restic_server
      caddy           = var.enable_caddy
      xtdb            = var.enable_xtdb
      docker_registry = local.registry_count > 0
    }

    automations_enabled = {
      fx_notify    = var.enable_fx_notify
      fx_tusd_hook = var.enable_fx_tusd_hook
    }
  }
}
