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
    prometheus      = local.prometheus_count > 0 ? nomad_job.prometheus[0].id : "disabled"
    loki            = local.loki_count > 0 ? nomad_job.loki[0].id : "disabled"
    grafana         = local.grafana_count > 0 ? nomad_job.grafana[0].id : "disabled"
    alloy           = local.alloy_count > 0 ? nomad_job.alloy[0].id : "disabled"
    ntfy            = var.enable_ntfy ? nomad_job.ntfy[0].id : "disabled"
    job_notifier    = var.enable_job_notifier ? nomad_job.job_notifier[0].id : "disabled"
    boundary_worker = local.boundary_count > 0 ? nomad_job.boundary_worker[0].id : "disabled"
    docker_registry = local.registry_count > 0 ? nomad_job.docker_registry[0].id : "disabled"
  }
}

# ── Experimental tier ──────────────────────────────────────────────────────

output "experimental_services" {
  description = "Experimental-tier services in abc-experimental (all disabled by default)"
  value = {
    postgres      = var.enable_postgres ? nomad_job.postgres[0].id : "disabled"
    redis         = var.enable_redis ? nomad_job.redis[0].id : "disabled"
    wave          = var.enable_wave ? nomad_job.wave[0].id : "disabled"
    supabase      = var.enable_supabase ? nomad_job.supabase[0].id : "disabled"
    restic_server = var.enable_restic_server ? nomad_job.restic_server[0].id : "disabled"
    caddy         = var.enable_caddy ? nomad_job.caddy[0].id : "disabled"
    xtdb          = var.enable_xtdb ? nomad_job.xtdb[0].id : "disabled"
  }
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
    supabase      = var.enable_supabase ? "http://supabase.aither" : "disabled"
    caddy         = var.enable_caddy ? "http://${var.cluster_public_host}:2015  https://${var.cluster_public_host}:443" : "disabled"
    xtdb_http     = var.enable_xtdb ? "http://${var.cluster_tailscale_ip}:${var.xtdb_http_port}  (vhost: http://xtdb.aither)" : "disabled"
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
      docker_registry = local.registry_count > 0
    }

    experimental_enabled = {
      postgres      = var.enable_postgres
      redis         = var.enable_redis
      wave          = var.enable_wave
      supabase      = var.enable_supabase
      restic_server = var.enable_restic_server
      caddy         = var.enable_caddy
      xtdb          = var.enable_xtdb
    }

    automations_enabled = {
      fx_notify    = var.enable_fx_notify
      fx_tusd_hook = var.enable_fx_tusd_hook
    }
  }
}
