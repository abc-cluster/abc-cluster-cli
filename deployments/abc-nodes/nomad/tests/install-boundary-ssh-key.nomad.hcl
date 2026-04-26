# install-boundary-ssh-key.nomad.hcl
#
# System batch job — runs once on EVERY eligible Nomad client node.
# Installs the Boundary-brokered SSH public key for user "abhinavsharma"
# into ~/.ssh/authorized_keys so Boundary workers can proxy SSH sessions
# to each node.
#
# Deploy:
#   nomad job run deployments/abc-nodes/nomad/tests/install-boundary-ssh-key.nomad.hcl
#
# The public key corresponds to the private key stored in Vault at
# ssh-creds/data/ubuntu-aither (and per-node paths ssh-creds/data/<node>).
#
# Idempotent: skips the key if it is already present in authorized_keys.

variable "ssh_user" {
  type    = string
  default = "abhinavsharma"
}

variable "ssh_pubkey" {
  type        = string
  description = "Public key to install (must match private key in Vault ssh-creds)"
  default     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKKq/nGuytY3yFog+oN1KEwJJ2m49KPQ8OFL/JTedPZa boundary-brokered@abc-nodes"
}

job "install-boundary-ssh-key" {
  namespace   = "default"
  region      = "global"
  datacenters = ["*"]
  type        = "batch"

  meta {
    purpose     = "boundary-ssh-key-install"
    key_comment = "boundary-brokered@abc-nodes"
  }

  # One task group per node — ensures every eligible Nomad client gets the key.
  # raw_exec runs as root so it can write to any user's authorized_keys.

  group "install-aither" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-nomad00" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad00"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-nomad01" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad01"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-nomad02" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad02"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-nomad03" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad03"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-nomad04" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad04"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "install-oci" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "oci-abhi-phd-arm-sa"
    }
    task "install-key" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args    = ["-c", "SSH_USER='${var.ssh_user}'; PUBKEY='${var.ssh_pubkey}'; HOME_DIR=$(getent passwd \"$SSH_USER\" | cut -d: -f6 2>/dev/null || echo \"/home/$SSH_USER\"); mkdir -p \"$HOME_DIR/.ssh\"; chmod 700 \"$HOME_DIR/.ssh\"; KEY_FP=$(echo \"$PUBKEY\" | awk '{print $2}'); if grep -qF \"$KEY_FP\" \"$HOME_DIR/.ssh/authorized_keys\" 2>/dev/null; then echo \"OK: key already present on $(hostname) for $SSH_USER\"; else echo \"$PUBKEY\" >> \"$HOME_DIR/.ssh/authorized_keys\"; chmod 600 \"$HOME_DIR/.ssh/authorized_keys\"; echo \"INSTALLED: key added on $(hostname) for $SSH_USER\"; fi"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }
}
