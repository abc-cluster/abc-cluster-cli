---
sidebar_position: 11
---

# workbench

Inspect and interact with the interactive computing environment (JupyterHub) running
on the ABC cluster.

## Basic usage

```bash
abc workbench url             # Print the JupyterHub URL
abc workbench start           # Admin/legacy: start a Docker session (see note)
abc workbench stop            # Admin/legacy: stop a Docker or VM session
abc workbench status          # Admin/legacy: show session details from local.db
abc workbench logs            # Admin/legacy: stream logs from a Docker/VM session
```

## JupyterHub — the primary workbench experience

The workbench for abc-seedling runs on **JupyterHub**. Pool users interact through
the browser — no `abc workbench start` required.

```
https://workbench.seedling.abc-cluster.cloud
```

Log in with your pool username and password. Click **Start My Server** to launch
a JupyterLab session. Your home directory (`/data/workbench/<user>/home/`) is
persisted — files, notebooks, and installed packages survive server stop/start.

JupyterHub spawns your JupyterLab server as a Nomad Docker job on aither.
MinIO credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `S3_ENDPOINT_URL`)
are injected automatically so `mc` and `s5cmd` work without configuration.

---

## `abc workbench url`

Print the workbench URL. In JupyterHub mode this is always the hub landing page.

```bash
abc workbench url
```

Output:

```
  Browser: https://workbench.seedling.abc-cluster.cloud
  Log in with your pool username. Your server starts automatically.
```

---

## Admin / legacy subcommands

The following subcommands manage **Docker** and **VM** backend sessions — used by
admins for development and testing. They are not the user-facing workbench at seedling.

### `abc workbench start`

Start an admin Docker or VM workbench session.

```bash
abc workbench start --backend=docker   # Nomad service job (container)
abc workbench start --backend=vm       # Multipass VM
```

| Flag | Default | Description |
|---|---|---|
| `--backend` | `docker` | Backend: `docker` or `vm` |
| `--cores N` | `2` | CPU cores |
| `--mem N` | `4096` | Memory in MB |
| `--idle-hours N` | `4` | Auto-stop after N idle hours (0 = no timeout) |
| `--ide NAME` | `quarto` | IDE image: `quarto` or `code-server` |
| `--project PATH` | — | Open this directory in the IDE on startup |
| `--node NAME` | platform node | Pin session to a specific Nomad node |
| `--no-telemetry` | — | Disable session-watcher sidecar |

### `abc workbench stop`

Stop a running Docker or VM session.

```bash
abc workbench stop
```

For the VM backend, the VM is suspended (state preserved). For the Docker backend,
the Nomad job is deregistered. The Caddy admin API route is removed in both cases.

### `abc workbench status`

Show session details from the local database (`~/.abc/db/local.db`).

```bash
abc workbench status
```

### `abc workbench logs`

Stream logs from the running Docker or VM session.

```bash
abc workbench logs
```

---

## Persistent storage

| Location | What it holds | Survives stop? |
|---|---|---|
| `~/` inside JupyterLab | Notebooks, installed packages, dotfiles | ✅ (bind-mounted from `/data/workbench/<user>/home/` on aither) |
| `s3://<user>/` | Pipeline outputs, uploaded data | ✅ (MinIO, independent of workbench) |

The home directory is stored at `/data/workbench/<user>/home/` on aither. It is
bind-mounted into every session for the same user so packages installed in one
session are available in the next.

> **Note:** The home directory is created automatically when your first server
> starts. Admins do not need to pre-create it.

---

## Adding MinIO credentials for a new user

Admins provision per-user MinIO credentials on aither before the user can access
MinIO from inside JupyterLab:

On aither (as root or with sudo), create `/etc/jupyterhub/minio-creds/<username>` with:

```ini title="/etc/jupyterhub/minio-creds/<username>"
AWS_ACCESS_KEY_ID=<access_key>
AWS_SECRET_ACCESS_KEY=<secret_key>
```

Then lock it down:

```bash
chmod 600 /etc/jupyterhub/minio-creds/<username>
```

These are injected automatically into the user's JupyterLab environment on next
server start.

---

## Architecture reference

```
browser → HTTPS (GCP Caddy) → workbench.seedling.abc-cluster.cloud
       → aither:9080 (caddy-workbench, static proxy)
       → localhost:8000 (JupyterHub + configurable-http-proxy)
       → /user/<username>/* → per-user JupyterLab (Nomad Docker job on aither)
```

JupyterHub runs as a Nomad `raw_exec` job (`abc-jupyterhub`) pinned to aither.
NomadSpawner (`jupyterhub-nomad-spawner`) submits and stops per-user Nomad jobs.
See `abc-deployments/abc-seedling-prod/jupyterhub/` for all deployment files.
