# create-boundary-ssh-user.nomad.hcl
#
# System batch job — runs once on EVERY eligible Nomad client node.
# Creates the "abhinavsharma" user for Boundary-brokered SSH sessions.
#
# The Boundary credential library uses the private key from Vault at
# ssh-creds/data/ubuntu-aither; the matching public key was installed by
# install-boundary-ssh-key.nomad.hcl.  If the user does not exist on a node,
# SSH authentication will fail even if the key is present.
#
# Deploy:
#   nomad job run deployments/abc-nodes/nomad/tests/create-boundary-ssh-user.nomad.hcl
#
# Idempotent: uses `id -u` to skip creation if user already exists.

variable "ssh_user" {
  type    = string
  default = "abhinavsharma"
}

variable "ssh_pubkey" {
  type        = string
  description = "Public key to install (must match private key in Vault ssh-creds)"
  default     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKKq/nGuytY3yFog+oN1KEwJJ2m49KPQ8OFL/JTedPZa boundary-brokered@abc-nodes"
}

job "create-boundary-ssh-user" {
  namespace   = "default"
  region      = "global"
  datacenters = ["*"]
  type        = "batch"

  meta {
    purpose     = "boundary-ssh-user-create"
    key_comment = "boundary-brokered@abc-nodes"
  }

  # Create user on each nomad node (nomad00-04 only — aither already has abhinavsharma).
  # raw_exec runs as root so it can create users and fix SSH key ownership.

  group "create-nomad00" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad00"
    }
    task "create-user" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args = ["-c", <<-EOF
          SSH_USER='${var.ssh_user}'
          PUBKEY='${var.ssh_pubkey}'
          if id -u "$SSH_USER" >/dev/null 2>&1; then
            echo "OK: user $SSH_USER already exists on $(hostname)"
          else
            useradd -m -s /bin/bash "$SSH_USER"
            echo "CREATED: user $SSH_USER on $(hostname)"
          fi
          HOME_DIR=$(getent passwd "$SSH_USER" | cut -d: -f6)
          mkdir -p "$HOME_DIR/.ssh"
          chmod 700 "$HOME_DIR/.ssh"
          chown -R "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh"
          KEY_FP=$(echo "$PUBKEY" | awk '{print $2}')
          if grep -qF "$KEY_FP" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
            echo "OK: SSH key already present for $SSH_USER on $(hostname)"
          else
            echo "$PUBKEY" >> "$HOME_DIR/.ssh/authorized_keys"
            chmod 600 "$HOME_DIR/.ssh/authorized_keys"
            chown "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh/authorized_keys"
            echo "INSTALLED: SSH key for $SSH_USER on $(hostname)"
          fi
        EOF
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "create-nomad01" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad01"
    }
    task "create-user" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args = ["-c", <<-EOF
          SSH_USER='${var.ssh_user}'
          PUBKEY='${var.ssh_pubkey}'
          if id -u "$SSH_USER" >/dev/null 2>&1; then
            echo "OK: user $SSH_USER already exists on $(hostname)"
          else
            useradd -m -s /bin/bash "$SSH_USER"
            echo "CREATED: user $SSH_USER on $(hostname)"
          fi
          HOME_DIR=$(getent passwd "$SSH_USER" | cut -d: -f6)
          mkdir -p "$HOME_DIR/.ssh"
          chmod 700 "$HOME_DIR/.ssh"
          chown -R "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh"
          KEY_FP=$(echo "$PUBKEY" | awk '{print $2}')
          if grep -qF "$KEY_FP" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
            echo "OK: SSH key already present for $SSH_USER on $(hostname)"
          else
            echo "$PUBKEY" >> "$HOME_DIR/.ssh/authorized_keys"
            chmod 600 "$HOME_DIR/.ssh/authorized_keys"
            chown "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh/authorized_keys"
            echo "INSTALLED: SSH key for $SSH_USER on $(hostname)"
          fi
        EOF
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "create-nomad02" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad02"
    }
    task "create-user" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args = ["-c", <<-EOF
          SSH_USER='${var.ssh_user}'
          PUBKEY='${var.ssh_pubkey}'
          if id -u "$SSH_USER" >/dev/null 2>&1; then
            echo "OK: user $SSH_USER already exists on $(hostname)"
          else
            useradd -m -s /bin/bash "$SSH_USER"
            echo "CREATED: user $SSH_USER on $(hostname)"
          fi
          HOME_DIR=$(getent passwd "$SSH_USER" | cut -d: -f6)
          mkdir -p "$HOME_DIR/.ssh"
          chmod 700 "$HOME_DIR/.ssh"
          chown -R "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh"
          KEY_FP=$(echo "$PUBKEY" | awk '{print $2}')
          if grep -qF "$KEY_FP" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
            echo "OK: SSH key already present for $SSH_USER on $(hostname)"
          else
            echo "$PUBKEY" >> "$HOME_DIR/.ssh/authorized_keys"
            chmod 600 "$HOME_DIR/.ssh/authorized_keys"
            chown "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh/authorized_keys"
            echo "INSTALLED: SSH key for $SSH_USER on $(hostname)"
          fi
        EOF
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "create-nomad03" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad03"
    }
    task "create-user" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args = ["-c", <<-EOF
          SSH_USER='${var.ssh_user}'
          PUBKEY='${var.ssh_pubkey}'
          if id -u "$SSH_USER" >/dev/null 2>&1; then
            echo "OK: user $SSH_USER already exists on $(hostname)"
          else
            useradd -m -s /bin/bash "$SSH_USER"
            echo "CREATED: user $SSH_USER on $(hostname)"
          fi
          HOME_DIR=$(getent passwd "$SSH_USER" | cut -d: -f6)
          mkdir -p "$HOME_DIR/.ssh"
          chmod 700 "$HOME_DIR/.ssh"
          chown -R "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh"
          KEY_FP=$(echo "$PUBKEY" | awk '{print $2}')
          if grep -qF "$KEY_FP" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
            echo "OK: SSH key already present for $SSH_USER on $(hostname)"
          else
            echo "$PUBKEY" >> "$HOME_DIR/.ssh/authorized_keys"
            chmod 600 "$HOME_DIR/.ssh/authorized_keys"
            chown "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh/authorized_keys"
            echo "INSTALLED: SSH key for $SSH_USER on $(hostname)"
          fi
        EOF
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  group "create-nomad04" {
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad04"
    }
    task "create-user" {
      driver = "raw_exec"
      config {
        command = "/bin/bash"
        args = ["-c", <<-EOF
          SSH_USER='${var.ssh_user}'
          PUBKEY='${var.ssh_pubkey}'
          if id -u "$SSH_USER" >/dev/null 2>&1; then
            echo "OK: user $SSH_USER already exists on $(hostname)"
          else
            useradd -m -s /bin/bash "$SSH_USER"
            echo "CREATED: user $SSH_USER on $(hostname)"
          fi
          HOME_DIR=$(getent passwd "$SSH_USER" | cut -d: -f6)
          mkdir -p "$HOME_DIR/.ssh"
          chmod 700 "$HOME_DIR/.ssh"
          chown -R "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh"
          KEY_FP=$(echo "$PUBKEY" | awk '{print $2}')
          if grep -qF "$KEY_FP" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
            echo "OK: SSH key already present for $SSH_USER on $(hostname)"
          else
            echo "$PUBKEY" >> "$HOME_DIR/.ssh/authorized_keys"
            chmod 600 "$HOME_DIR/.ssh/authorized_keys"
            chown "$SSH_USER:$SSH_USER" "$HOME_DIR/.ssh/authorized_keys"
            echo "INSTALLED: SSH key for $SSH_USER on $(hostname)"
          fi
        EOF
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }
  }
}
