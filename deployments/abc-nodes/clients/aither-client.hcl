# aither-client.hcl — reference Nomad client config for the aither node.
#
# Deploy to /etc/nomad.d/client.hcl on aither and restart nomad:
#
#   scp deployments/abc-nodes/clients/aither-client.hcl aither:/etc/nomad.d/client.hcl
#   ssh aither sudo systemctl restart nomad
#
# IMPORTANT: also remove `-dev` from /etc/systemd/system/nomad.service
# (ExecStart line) before restart. `-dev` forces single-node-server-and-client
# mode and overrides this client-only config. The systemd unit should read:
#
#   ExecStart=/usr/local/bin/nomad agent -config /etc/nomad.d
#
# Tailscale MagicDNS note: replace <tailnet> below with your actual tailnet
# name (e.g. "example" → "nomad00.example.ts.net"). Find it at
# https://login.tailscale.com/admin/dns under MagicDNS domain.
# Fallback raw Tailscale IPs: nomad00=100.108.199.30, nomad01=100.77.21.36,
# oci-abhi-phd-arm-sa=100.68.49.95.
#
# This file is checked in as reference only — it is NOT deployed by a Nomad
# job or Terraform. See deployments/abc-nodes/clients/README.md.

datacenter = "default"
region     = "global"
name       = "aither"

client {
  enabled = true

  servers = [
    "nomad00.<tailnet>.ts.net",
    "nomad01.<tailnet>.ts.net",
    "oci-abhi-phd-arm-sa.<tailnet>.ts.net",
  ]

  options {
    "driver.allowlist" = "containerd-driver,docker,raw_exec,exec,java"
  }

  cni_path = "/opt/cni/bin"

  host_volume "scratch" {
    path      = "/opt/nomad/scratch"
    read_only = false
  }
}

plugin "containerd-driver" {
  config {
    enabled            = true
    containerd_runtime = "io.containerd.runc.v2"
    allow_privileged   = true
    auth {
      # Configure here if pulling from a private registry globally.
      # Per-job auth blocks (task driver config.auth stanza) also work and
      # are preferred for per-registry credentials.
    }
  }
}

plugin "docker" {
  # NOTE: the Docker driver plugin's config schema does NOT accept `enabled`
  # (unlike containerd-driver and raw_exec, which do). The plugin is enabled
  # by the presence of this block; setting `enabled = true` causes the agent
  # to fail with "Invalid label: No argument or block type is named 'enabled'."
  # See spec errata in $ABC_UNIVERSE/specs/completed/aither-as-client-and-lab.md.
  config {
    allow_privileged = true
    volumes {
      enabled = true   # required for jobs using the volumes stanza
    }
    gc {
      image       = true
      image_delay = "24h"   # GC pulled images after a day idle
      container   = true
    }
    auth {
      # Private-registry auth — typically handled per-job via the task's
      # config.auth block; no global credential needed unless all jobs share
      # one registry.
    }
  }
}

plugin "raw_exec" {
  config {
    enabled = true
  }
}
