---
title: Caddy configuration
---

# Caddy configuration

Caddy handles TLS (via ACME/Let's Encrypt), static file serving, and
reverse-proxying `/api/*` calls to the claim service. A template
Caddyfile is in `cloud-init/Caddyfile`.

## Template

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

Replace `your-cluster.example.com` with your actual hostname.

## Placement

On the edge VM, the Caddyfile is typically at one of:

- `/etc/caddy/Caddyfile` — system-wide (Caddy installed as a service)
- `~/Caddyfile` — run manually with `caddy run`

After editing, reload Caddy without dropping connections:

```bash
sudo systemctl reload caddy
# or, if running manually:
caddy reload --config /path/to/Caddyfile
```

## DNS requirements

The hostname must resolve to the VM's public IP **before** Caddy first
starts with that site block, so the ACME challenge can succeed. Caddy will
obtain and renew TLS certificates automatically via Let's Encrypt HTTP-01
challenge.

```bash
# Verify DNS is resolving to the right IP before starting Caddy:
dig +short your-cluster.example.com
curl -v http://your-cluster.example.com/healthz  # should redirect to HTTPS
```

## Port requirements

The edge VM must accept:
- TCP 80 — ACME HTTP-01 challenge (Caddy redirects to HTTPS after)
- TCP 443 — HTTPS
- TCP 22 — SSH (for deployment)

The claim service (`abc-landing-api`) listens on `localhost:8081` and is
never exposed directly to the internet — only via the Caddy reverse proxy.

## Service consoles

The cluster's service consoles (Nomad UI, MinIO console, Grafana) are
separate virtual hosts. They can be added to the same Caddyfile or a
separate one:

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

## Troubleshooting

**ACME certificate fails:** check that port 80 is reachable and DNS resolves
correctly. `sudo journalctl -u caddy -f` shows the ACME exchange.

**`/api/*` returns 502:** the claim service is not running. Check the Nomad
job status: `abc admin services nomad cli -- job status abc-landing-api`.

**Landing page shows `REPLACE_` tokens:** `deploy-landing.sh` was not run,
or the raw template files were copied directly. Always deploy through the
script.

## Next step

→ [Issuing handover files](handover)
