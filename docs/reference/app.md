---
sidebar_position: 11
---

# app

Deploy a browser-facing scientific web app — **Streamlit**, **R Shiny**, or **Pode.Web** — as a persistent, auth-gated service on the cluster, described by a single `abc-app.yaml`. The CLI handles the Nomad job, subdomain routing, health checks, and per-app object-store credentials; you do not write Nomad HCL, router config, or policy by hand.

Apps are reachable at a stable subdomain (`https://<project>-<name>.apps.<tier>…`) and run at the container's root path. They stay up between sessions — unlike a notebook server, an app does not stop when you log out.

## Quick start

```bash
abc app init --framework streamlit --with-dockerfile   # scaffold abc-app.yaml (+ Dockerfile)
# ...build & push your image, set `image:` in abc-app.yaml...
abc app validate          # check the descriptor offline
abc app deploy            # deploy; prints the URL when healthy
abc app open              # open it in your browser
abc app logs -f           # tail the container logs
```

## `abc-app.yaml`

```yaml
version: "1.0"             # abc-app.yaml schema version (kept first; legacy "1" normalises)
name: tb-viewer            # short app name (kebab-case); addressed as `abc app <name>`
project: mtb-resistotyper-ml   # group you deploy as (access: team is scoped to it)
framework: streamlit       # streamlit | shiny | pode | custom
image: ghcr.io/your-org/tb-viewer:1.0.0   # pre-built OCI image (no source build)

port: 8501                 # container listen port (must bind 0.0.0.0)
health: /_stcore/health    # HTTP path the platform polls for readiness

access: team               # WHO can reach it (auth): team only (cluster/public reserved)
exposure: public           # WHICH networks: public (default) | internal | both

resources:                 # hard limits (defaults: cpu 500 MHz, memory 1024 MiB)
  cpu: 1000
  memory: 2048

env:                       # plain (non-secret) env injected into the container
  LOG_LEVEL: info          # ABC_*/AWS_* are platform-injected and cannot be set here

data:                      # MinIO buckets the app reads (Vault-minted AWS_* creds injected)
  - bucket: su-mtb-resistotyper-ml
    access: read           # read (default) or read-write
```

**Containerisation is required** — apps ship as pre-built OCI images. There is no source-based build path; build and push the image yourself, then reference it in `image:`.

**Bind contract** — the container must listen on `0.0.0.0` at the declared `port`, or the health check fails. Framework run commands:

| Framework | default `port` / `health` | run command must bind |
|---|---|---|
| `streamlit` | 8501 / `/_stcore/health` | `streamlit run app.py --server.address=0.0.0.0 --server.port=8501 --server.headless=true` |
| `shiny` | 3838 / `/` | `options(shiny.host="0.0.0.0", shiny.port=3838)` |
| `pode` | 8085 / `/health/live` | bind `0.0.0.0:8085` |
| `custom` | (required) | your responsibility |

`abc app init --with-dockerfile` writes a starter `Dockerfile` that already honours this.

## Exposure — which networks can reach the app

`exposure` is the **network-reach** axis, independent of `access` (which is *auth*). Every combination is valid — e.g. `internal` + `team` is institution-only **and** authenticated.

| `exposure` | Reach |
|---|---|
| `public` (default) | served at the public subdomain — internet-reachable |
| `internal` | **off the public edge** — reachable only from inside the institution (Tailscale + campus LAN) |
| `both` | both |

```bash
abc app deploy --exposure internal      # institution-only (override the descriptor)
```

For `exposure: internal`, the app is **not** published at the public `*.apps.<tier>` subdomain; reach it over Tailscale (MagicDNS / Tailscale Serve) or the campus LAN. An internal app can also be served directly on a dedicated host port for fully DNS-free LAN access — see the operator runbook (`abc-deployments/abc-seedling-prod/docs/internal-app-exposure.md`).

## Commands

| Command | Description |
|---|---|
| `abc app init [--framework F] [--name N] [--project P] [--with-dockerfile] [--force]` | Scaffold an `abc-app.yaml` (and optional `Dockerfile`) in the cwd |
| `abc app validate [-f FILE] [--canonical]` | Validate the descriptor + print resolved values (offline); `--canonical` prints the version-first, key-sorted YAML (pipe to re-sort a descriptor) |
| `abc app deploy [-f FILE] [--image TAG] [--exposure E] [--node-pool P] [--dry-run] [--no-wait]` | Deploy/update; polls health unless `--no-wait`; `--dry-run` prints the Nomad HCL. `--exposure` overrides the descriptor; `--node-pool` overrides the context's head pool |
| `abc app list` | Table of deployed apps: name, status, project, image, uptime, URL |
| `abc app show <name>` | One app's status, alloc, URL, image, resources, data buckets |
| `abc app logs <name> [-f] [--tail N] [--since D]` | Stream container logs; `-f` follows live |
| `abc app open <name> [--print]` | Open the app URL in the default browser (`--print` just prints it) |
| `abc app url <name>` | Print the app URL only (pipe-friendly) |
| `abc app config list\|get\|set\|unset <name> [KEY=VAL…]` | View/edit plain env vars (re-rolls on change) |
| `abc app exec <name> [-it] -- <cmd…>` | Run a one-off command in the running container |
| `abc app restart <name> [--no-wait]` | Re-roll the running allocation (no spec change) |
| `abc app stop <name>` | Stop the app; preserves the job definition |
| `abc app delete <name> --confirm` | Delete the app + revoke its data credentials |

### `config` — environment variables

```bash
abc app config list tb-viewer
abc app config set  tb-viewer LOG_LEVEL=debug PAGE_TITLE="TB Viewer"   # re-rolls the app
abc app config get  tb-viewer LOG_LEVEL
abc app config unset tb-viewer PAGE_TITLE
```

Platform-injected variables (`ABC_*`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `ABC_MINIO_ENDPOINT`) are owned by the platform — attempting to set or unset them is rejected. They are derived from your `project` and `data:` configuration.

### `exec` — one-off commands

```bash
abc app exec tb-viewer -- python -V
abc app exec tb-viewer -it -- /bin/sh        # interactive shell
```

## How it works

- **Routing.** Each app registers a cluster service tagged for the app router, which exposes it at `https://<project>-<name>.apps.<tier>…` and serves it at root — no path prefix, no per-framework base-URL configuration.
- **Auth.** App routes are auth-gated; `access: team` restricts access to members of the app's `project` group. (`cluster`/`public` are reserved.)
- **Data.** Each `data:` bucket gets a per-app, scoped object-store service account; its credentials are injected as `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `ABC_MINIO_ENDPOINT`. `abc app delete` revokes them.
- **Provenance.** The app reads pipeline outputs from object storage; it does not provision its own database. The data plane is the cluster's.

## See also

- [pipeline run](./pipeline) — produce the outputs an app reads
- [workbench](./workbench) — interactive sessions for developing an app before deploying it
