# abc-node-probe-periodic.nomad.hcl
#
# Periodic system batch — runs the abc-node-probe binary on every cluster
# node nightly. Results are appended to a host-local JSON-lines file at
# /var/lib/abc/probe-history.jsonl and (when the pushgateway is reachable)
# pushed to Prometheus for time-series visibility of node health.
#
# Phase 1 of the abc-node-probe improvement plan — see
# specs/active/abc-node-probe-phase-0-1.md and
# brainstorms/abc-node-probe-improvements/2026-05-08-hpc-monitoring-gaps.md
# in the abc-universe repo.
#
# What the periodic execution gives you that one-shot doesn't
# ───────────────────────────────────────────────────────────
#  - ECC error accumulation over time (a node that started accumulating
#    uncorrectable ECC three weeks ago shows up as a step-change in the
#    Prometheus series, not a one-shot Pass/Warn/Fail at admission)
#  - InfiniBand link-rate drift (cable flapping that demotes 200 Gbps →
#    100 Gbps becomes visible)
#  - NVMe wear progression
#  - Driver / kernel update detection (capability surface changes between
#    runs)
#  - Memory bandwidth trends (thermal-throttling under sustained load
#    shows up as a sagging GB/s series)
#
# Prerequisites
# ─────────────
# 1. The probe binary is on PATH at /opt/abc/bin/abc-node-probe (or use
#    the META.binary_path override below).
# 2. /var/lib/abc/ is writable by the user the Nomad client runs as
#    (typically root for this kind of operational data).
# 3. (Optional) A Prometheus pushgateway is reachable at the address
#    given by META.pushgateway. Set to empty string to disable.
#
# Deploy
# ──────
#   abc admin services nomad cli -- run \
#     deployments/abc-nodes/nomad/probe/abc-node-probe-periodic.nomad.hcl

job "abc-node-probe-periodic" {
  type        = "sysbatch"
  datacenters = ["dc1"]
  namespace   = "abc-automations"

  # Run once per node, nightly at 03:00 UTC. prohibit_overlap ensures a
  # long-running probe (rare; the bench checks total ~10s) doesn't cause
  # overlapping invocations on the same node.
  periodic {
    crons             = ["0 3 * * *"]
    prohibit_overlap  = true
    time_zone         = "UTC"
  }

  meta {
    # Override these per cluster as needed.
    binary_path  = "/opt/abc/bin/abc-node-probe"
    history_file = "/var/lib/abc/probe-history.jsonl"
    pushgateway  = "http://pushgateway.service.consul:9091"
    jurisdiction = "ZA"
  }

  group "probe" {
    # No count for sysbatch — runs once per eligible node.
    restart {
      attempts = 1
      interval = "10m"
      delay    = "30s"
      mode     = "fail"
    }

    task "run" {
      driver = "raw_exec"

      config {
        command = "${NOMAD_META_binary_path}"
        args = [
          "--jurisdiction=${NOMAD_META_jurisdiction}",
          "--quiet",
          "--evaluate",
          "--nomad-mode",
          "--mode=stdout",
          "--history-file=${NOMAD_META_history_file}",
          "--push-prometheus=${NOMAD_META_pushgateway}",
          "--timeout=2m",
        ]
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
