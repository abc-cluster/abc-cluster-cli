# install-consul-via-nomad.nomad.hcl
#
# One-shot Nomad batch job that installs Consul CLIENT on each Nomad server node.
# Runs as root (via raw_exec) so no sudo password required.
#
# Target nodes: nomad00, nomad01, oci-abhi-phd-arm-sa
# (The three Nomad Raft server nodes that don't yet have Consul)
#
# Run:
#   abc admin services nomad cli -- job run \
#     deployments/abc-nodes/consul/install-consul-via-nomad.nomad.hcl

variable "consul_version" {
  type    = string
  default = "1.19.2"
}

# Consul server (aither) Tailscale IP
variable "consul_server_ip" {
  type    = string
  default = "100.70.185.46"
}

variable "aither_lan_ip" {
  type    = string
  default = "146.232.174.77"
}

job "install-consul-on-server-nodes" {
  namespace   = "default"
  region      = "global"
  datacenters = ["sun-nomadlab", "oci-nomadlab", "default"]
  type        = "batch"

  # nomad00 task group
  group "nomad00" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad00"
    }

    task "install" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["${NOMAD_TASK_DIR}/install-consul.sh"]
      }

      template {
        data        = <<EOF
#!/bin/bash
set -euo pipefail
NODE_IP="{{ env "attr.unique.network.ip-address" }}"
CONSUL_VERSION="${var.consul_version}"
CONSUL_SERVER="${var.consul_server_ip}"
AITHER_LAN_IP="${var.aither_lan_ip}"

echo "==> Installing Consul ${CONSUL_VERSION} on $(hostname) (${NODE_IP})"

# Install Consul binary
if consul version 2>/dev/null | grep -q "${CONSUL_VERSION}"; then
  echo "    Already at ${CONSUL_VERSION} — skipping download"
else
  cd /tmp
  ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -fsSL "https://releases.hashicorp.com/consul/${CONSUL_VERSION}/consul_${CONSUL_VERSION}_linux_${ARCH}.zip" -o consul.zip
  unzip -o consul.zip consul
  install -o root -g root -m 0755 consul /usr/local/bin/consul
  rm -f consul consul.zip
  echo "    Consul $(consul version | head -1) installed"
fi

# Create consul user and dirs
if ! id consul &>/dev/null; then
  useradd --system --home /etc/consul.d --shell /bin/false consul
fi
mkdir -p /etc/consul.d /opt/consul /var/log/consul
chown -R consul:consul /etc/consul.d /opt/consul /var/log/consul

# Write consul-client.hcl
cat > /etc/consul.d/consul.hcl << CONSUL_CONF
datacenter = "dc1"
data_dir   = "/opt/consul"
server     = false

bind_addr      = "${NODE_IP}"
client_addr    = "127.0.0.1 ${NODE_IP}"
advertise_addr = "${NODE_IP}"

retry_join = ["${CONSUL_SERVER}"]

ports {
  http = 8500
  dns  = 8600
}

log_level = "INFO"

acl {
  enabled = false
}
CONSUL_CONF
chown consul:consul /etc/consul.d/consul.hcl
chmod 640 /etc/consul.d/consul.hcl

# Write systemd unit
cat > /etc/systemd/system/consul.service << 'SYSTEMD'
[Unit]
Description=HashiCorp Consul
Documentation=https://www.consul.io/
Requires=network-online.target
After=network-online.target
ConditionFileNotEmpty=/etc/consul.d/consul.hcl

[Service]
Type=exec
User=consul
Group=consul
ExecStart=/usr/local/bin/consul agent -config-dir=/etc/consul.d/
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SYSTEMD
systemctl daemon-reload

# Start Consul
systemctl enable consul
systemctl restart consul
sleep 5
systemctl is-active consul || { journalctl -u consul -n 20 --no-pager; exit 1; }
echo "==> Consul started"

# Install dnsmasq
apt-get install -y -qq dnsmasq 2>/dev/null || true
cat > /etc/dnsmasq.d/10-consul.conf << 'DNS'
server=/consul/127.0.0.1#8600
no-negcache
DNS
cat > /etc/dnsmasq.d/20-aither.conf << DNS
address=/.aither/${AITHER_LAN_IP}
DNS
cat > /etc/dnsmasq.d/00-listen.conf << 'DNS'
listen-address=127.0.0.1
bind-interfaces
DNS
UPSTREAM=$(systemd-resolve --status 2>/dev/null | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' || echo "8.8.8.8")
cat > /etc/dnsmasq.d/30-upstream.conf << DNS
server=${UPSTREAM}
DNS
mkdir -p /etc/systemd/resolved.conf.d
cat > /etc/systemd/resolved.conf.d/no-stub.conf << 'RCONF'
[Resolve]
DNSStubListener=no
RCONF
systemctl restart systemd-resolved 2>/dev/null || true
systemctl enable dnsmasq
systemctl restart dnsmasq
chattr -i /etc/resolv.conf 2>/dev/null || true
rm -f /etc/resolv.conf
echo "nameserver 127.0.0.1" > /etc/resolv.conf
chattr +i /etc/resolv.conf
echo "==> dnsmasq configured"

# Add Nomad consul stanza
if [[ ! -f /etc/nomad.d/consul.hcl ]]; then
  cat > /etc/nomad.d/consul.hcl << 'NC'
consul {
  address = "127.0.0.1:8500"
}
NC
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
fi

echo "==> consul members:"
consul members || true
echo "==> Consul install complete on $(hostname)"
EOF
        destination = "local/install-consul.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }

  # nomad01 task group
  group "nomad01" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "nomad01"
    }

    task "install" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["${NOMAD_TASK_DIR}/install-consul.sh"]
      }

      template {
        data        = <<EOF
#!/bin/bash
set -euo pipefail
NODE_IP="{{ env "attr.unique.network.ip-address" }}"
CONSUL_VERSION="${var.consul_version}"
CONSUL_SERVER="${var.consul_server_ip}"
AITHER_LAN_IP="${var.aither_lan_ip}"

echo "==> Installing Consul ${CONSUL_VERSION} on $(hostname) (${NODE_IP})"

if consul version 2>/dev/null | grep -q "${CONSUL_VERSION}"; then
  echo "    Already at ${CONSUL_VERSION} — skipping download"
else
  cd /tmp
  ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -fsSL "https://releases.hashicorp.com/consul/${CONSUL_VERSION}/consul_${CONSUL_VERSION}_linux_${ARCH}.zip" -o consul.zip
  unzip -o consul.zip consul
  install -o root -g root -m 0755 consul /usr/local/bin/consul
  rm -f consul consul.zip
fi

if ! id consul &>/dev/null; then
  useradd --system --home /etc/consul.d --shell /bin/false consul
fi
mkdir -p /etc/consul.d /opt/consul /var/log/consul
chown -R consul:consul /etc/consul.d /opt/consul /var/log/consul

cat > /etc/consul.d/consul.hcl << CONSUL_CONF
datacenter = "dc1"
data_dir   = "/opt/consul"
server     = false

bind_addr      = "${NODE_IP}"
client_addr    = "127.0.0.1 ${NODE_IP}"
advertise_addr = "${NODE_IP}"

retry_join = ["${CONSUL_SERVER}"]

ports {
  http = 8500
  dns  = 8600
}

log_level = "INFO"

acl {
  enabled = false
}
CONSUL_CONF
chown consul:consul /etc/consul.d/consul.hcl
chmod 640 /etc/consul.d/consul.hcl

cat > /etc/systemd/system/consul.service << 'SYSTEMD'
[Unit]
Description=HashiCorp Consul
Documentation=https://www.consul.io/
Requires=network-online.target
After=network-online.target
ConditionFileNotEmpty=/etc/consul.d/consul.hcl

[Service]
Type=exec
User=consul
Group=consul
ExecStart=/usr/local/bin/consul agent -config-dir=/etc/consul.d/
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SYSTEMD
systemctl daemon-reload
systemctl enable consul
systemctl restart consul
sleep 5
systemctl is-active consul || { journalctl -u consul -n 20 --no-pager; exit 1; }

apt-get install -y -qq dnsmasq 2>/dev/null || true
cat > /etc/dnsmasq.d/10-consul.conf << 'DNS'
server=/consul/127.0.0.1#8600
no-negcache
DNS
cat > /etc/dnsmasq.d/20-aither.conf << DNS
address=/.aither/${AITHER_LAN_IP}
DNS
cat > /etc/dnsmasq.d/00-listen.conf << 'DNS'
listen-address=127.0.0.1
bind-interfaces
DNS
UPSTREAM=$(systemd-resolve --status 2>/dev/null | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' || echo "8.8.8.8")
cat > /etc/dnsmasq.d/30-upstream.conf << DNS
server=${UPSTREAM}
DNS
mkdir -p /etc/systemd/resolved.conf.d
cat > /etc/systemd/resolved.conf.d/no-stub.conf << 'RCONF'
[Resolve]
DNSStubListener=no
RCONF
systemctl restart systemd-resolved 2>/dev/null || true
systemctl enable dnsmasq
systemctl restart dnsmasq
chattr -i /etc/resolv.conf 2>/dev/null || true
rm -f /etc/resolv.conf
echo "nameserver 127.0.0.1" > /etc/resolv.conf
chattr +i /etc/resolv.conf

if [[ ! -f /etc/nomad.d/consul.hcl ]]; then
  cat > /etc/nomad.d/consul.hcl << 'NC'
consul {
  address = "127.0.0.1:8500"
}
NC
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
fi

echo "==> consul members:"
consul members || true
echo "==> Consul install complete on $(hostname)"
EOF
        destination = "local/install-consul.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }

  # oci-abhi-phd-arm-sa task group
  group "oci" {
    count = 1

    task "install" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["${NOMAD_TASK_DIR}/install-consul.sh"]
      }

      template {
        data        = <<EOF
#!/bin/bash
set -euo pipefail
NODE_IP="{{ env "attr.unique.network.ip-address" }}"
CONSUL_VERSION="${var.consul_version}"
CONSUL_SERVER="${var.consul_server_ip}"
AITHER_LAN_IP="${var.aither_lan_ip}"

echo "==> Installing Consul ${CONSUL_VERSION} on $(hostname) (${NODE_IP})"

if consul version 2>/dev/null | grep -q "${CONSUL_VERSION}"; then
  echo "    Already at ${CONSUL_VERSION} — skipping download"
else
  cd /tmp
  ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -fsSL "https://releases.hashicorp.com/consul/${CONSUL_VERSION}/consul_${CONSUL_VERSION}_linux_${ARCH}.zip" -o consul.zip
  unzip -o consul.zip consul
  install -o root -g root -m 0755 consul /usr/local/bin/consul
  rm -f consul consul.zip
fi

if ! id consul &>/dev/null; then
  useradd --system --home /etc/consul.d --shell /bin/false consul
fi
mkdir -p /etc/consul.d /opt/consul /var/log/consul
chown -R consul:consul /etc/consul.d /opt/consul /var/log/consul

cat > /etc/consul.d/consul.hcl << CONSUL_CONF
datacenter = "dc1"
data_dir   = "/opt/consul"
server     = false

bind_addr      = "${NODE_IP}"
client_addr    = "127.0.0.1 ${NODE_IP}"
advertise_addr = "${NODE_IP}"

retry_join = ["${CONSUL_SERVER}"]

ports {
  http = 8500
  dns  = 8600
}

log_level = "INFO"

acl {
  enabled = false
}
CONSUL_CONF
chown consul:consul /etc/consul.d/consul.hcl
chmod 640 /etc/consul.d/consul.hcl

cat > /etc/systemd/system/consul.service << 'SYSTEMD'
[Unit]
Description=HashiCorp Consul
Documentation=https://www.consul.io/
Requires=network-online.target
After=network-online.target
ConditionFileNotEmpty=/etc/consul.d/consul.hcl

[Service]
Type=exec
User=consul
Group=consul
ExecStart=/usr/local/bin/consul agent -config-dir=/etc/consul.d/
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SYSTEMD
systemctl daemon-reload
systemctl enable consul
systemctl restart consul
sleep 5
systemctl is-active consul || { journalctl -u consul -n 20 --no-pager; exit 1; }

# OCI uses either apt or yum
apt-get install -y -qq dnsmasq 2>/dev/null || yum install -y -q dnsmasq 2>/dev/null || true
cat > /etc/dnsmasq.d/10-consul.conf << 'DNS'
server=/consul/127.0.0.1#8600
no-negcache
DNS
cat > /etc/dnsmasq.d/20-aither.conf << DNS
address=/.aither/${AITHER_LAN_IP}
DNS
cat > /etc/dnsmasq.d/00-listen.conf << 'DNS'
listen-address=127.0.0.1
bind-interfaces
DNS
UPSTREAM=$(systemd-resolve --status 2>/dev/null | grep -m1 'DNS Servers' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' || echo "8.8.8.8")
cat > /etc/dnsmasq.d/30-upstream.conf << DNS
server=${UPSTREAM}
DNS
mkdir -p /etc/systemd/resolved.conf.d
cat > /etc/systemd/resolved.conf.d/no-stub.conf << 'RCONF'
[Resolve]
DNSStubListener=no
RCONF
systemctl restart systemd-resolved 2>/dev/null || true
systemctl enable dnsmasq
systemctl restart dnsmasq
chattr -i /etc/resolv.conf 2>/dev/null || true
rm -f /etc/resolv.conf
echo "nameserver 127.0.0.1" > /etc/resolv.conf
chattr +i /etc/resolv.conf

if [[ ! -f /etc/nomad.d/consul.hcl ]]; then
  cat > /etc/nomad.d/consul.hcl << 'NC'
consul {
  address = "127.0.0.1:8500"
}
NC
  systemctl reload nomad 2>/dev/null || systemctl restart nomad
fi

echo "==> consul members:"
consul members || true
echo "==> Consul install complete on $(hostname)"
EOF
        destination = "local/install-consul.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
