---
title: Reverse proxy / TLS (optional)
---

# Reverse proxy and TLS

A reverse proxy is **optional**. Whether you need one depends on where your
cluster is deployed:

| Deployment context | Recommendation |
|---|---|
| Public internet (DNS hostname, open ports 80/443) | **Caddy** — automatic TLS via ACME |
| Private LAN / HPC network / VPN-only | **No proxy needed** — serve HTTP directly from Nomad or a simple file server |
| Institutional network with a shared load balancer / WAF | **Defer to your network team** — terminate TLS upstream, forward plain HTTP to the cluster |

> **Best practice:** If your cluster is reachable from the public internet,
> use a reverse proxy to terminate TLS. Running credentials and claim codes
> over plain HTTP on a public network is not acceptable. On a private LAN
> where all traffic stays inside a trusted network boundary, HTTP is fine.

## Option A — No proxy (LAN / private network)

The claim service and landing files can be served directly without any proxy
layer. This is the simplest setup for clusters inside a university HPC
network, a research VPN, or an air-gapped environment.

### Serve landing files with Python (quick start)

```bash
# On the node that holds /var/www/<hostname>/
python3 -m http.server 8080 --directory /var/www/your-cluster.local/
```

Or serve them as a Nomad `exec` job alongside the claim service (both on
port 8080 and 8081 respectively, behind a simple iptables redirect from
port 80).

### Claim service

`claim_server.py` handles `/api/*` itself. For a plain HTTP setup, point
your landing page's `ABC_CLUSTER_HOSTNAME` to `your-node:8080` and ensure
the Caddy-style `/api/*` routing is not needed — the claim service and the
static files can share the same origin, or you can run a minimal Nginx or
Caddy config with no TLS block.

### Plain Caddy (no TLS)

Caddy can also serve HTTP-only (no ACME, no certificates) on a LAN by
binding to a bare IP or an internal hostname with no registered domain:

```caddy
:80 {
    root * /var/www/your-cluster.local

    handle /api/* {
        reverse_proxy localhost:8081
    }

    file_server
    encode gzip
}
```

Run with `caddy run --config Caddyfile`. No DNS or port 443 needed.

---

## Option B — Caddy with automatic TLS (internet-facing)

Use this when the cluster has a public DNS hostname and ports 80 + 443 are
reachable from the internet.

### Template Caddyfile

A template is in `cloud-init/Caddyfile`. Replace `REPLACE_CLUSTER_HOSTNAME`
with your actual hostname:

```caddy
your-cluster.example.com {
    root * /var/www/your-cluster.example.com

    # Proxy claim + feedback API to the Nomad claim service.
    handle /api/* {
        reverse_proxy localhost:8081
    }

    # Redirect legacy CLI doc paths that moved in the docs restructure.
    @oldcli path_regexp oldcli ^/docs/(quickstart|reference|tutorials|usage)(/.*)?$
    redir @oldcli /docs/cli/{re.oldcli.1}{re.oldcli.2} permanent

    file_server
    encode gzip

    header {
        X-Content-Type-Options        "nosniff"
        Referrer-Policy               "strict-origin-when-cross-origin"
        Strict-Transport-Security     "max-age=31536000; includeSubDomains"
        X-Frame-Options               "DENY"
        Permissions-Policy            "geolocation=(), microphone=(), camera=()"
    }
}
```

### Caddyfile placement

On the edge VM:

- `/etc/caddy/Caddyfile` — system-wide (Caddy installed as a service)
- `~/Caddyfile` — run manually with `caddy run`

After editing, reload Caddy without dropping connections:

```bash
sudo systemctl reload caddy
# or, if running manually:
caddy reload --config /path/to/Caddyfile
```

### DNS requirements

The hostname must resolve to the VM's public IP **before** Caddy first
starts with that site block, so the ACME HTTP-01 challenge can succeed.
Caddy obtains and renews TLS certificates automatically via Let's Encrypt.

```bash
# Verify DNS before starting Caddy:
dig +short your-cluster.example.com
curl -v http://your-cluster.example.com/healthz  # should redirect to HTTPS
```

### Port requirements

| Port | Purpose |
|---|---|
| TCP 80 | ACME HTTP-01 challenge; redirected to HTTPS by Caddy |
| TCP 443 | HTTPS |
| TCP 22 | SSH (deployment) |

The claim service on `localhost:8081` is never exposed directly — only via
the Caddy reverse proxy.

### Service consoles

Add the cluster's service UIs as separate virtual hosts in the same Caddyfile:

```caddy
nomad.your-cluster.example.com {
    reverse_proxy localhost:4646
}

minio.your-cluster.example.com {
    reverse_proxy localhost:9001
}

s3.your-cluster.example.com {
    reverse_proxy localhost:9000
}

upload.your-cluster.example.com {
    reverse_proxy localhost:1080  # tusd
}
```

---

## Option C — Upstream TLS termination

If your institution provides a shared load balancer, WAF, or TLS-terminating
reverse proxy (common at universities with an existing web gateway):

- Terminate TLS upstream.
- Forward plain HTTP to the cluster node on port 80 (or a custom port).
- On the cluster node, run Caddy or Python in plain HTTP mode (Option A).
- The `X-Forwarded-Proto: https` header set by the upstream proxy will be
  visible to the claim service if needed.

---

## Troubleshooting

**ACME certificate fails:** check that port 80 is reachable and DNS resolves
correctly. `sudo journalctl -u caddy -f` shows the ACME exchange.

**`/api/*` returns 502:** the claim service is not running. Check:
`abc admin services nomad cli -- job status abc-landing-api`

**Landing page shows `REPLACE_` tokens:** `deploy-landing.sh` was not run,
or the raw template files were copied directly. Always deploy through the
script.

## Next step

→ [Issuing handover files](handover)
