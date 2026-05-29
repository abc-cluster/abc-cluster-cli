---
title: Workbench (JupyterHub)
---

# Workbench — JupyterHub on seedling

The **abc-workbench** gives every pool user a persistent JupyterLab environment
in the browser. No software to install, no SSH keys to configure. Open the URL,
log in with your cluster credentials, and start working.

---

## For users

### Accessing the workbench

Navigate to the workbench URL for your cluster — your operator will give you
this address. On the public seedling instance it is:

```
https://workbench.seedling.abc-cluster.cloud
```

Log in with the **same username and password** from your `config.yaml` handover
file (the MinIO credentials). You do not need a separate workbench password.

### Starting your server

Click **Start My Server** after logging in. JupyterLab opens in a few seconds.
Your session runs as your own isolated pool slot — other users cannot see your
files or your terminal history.

### Persistent home directory

Everything you save under your home directory (`~`) is stored on the cluster
node and **persists across sessions**. If the operator stops your idle server
to free resources, your files are untouched. Simply log back in and click
Start My Server to resume.

Your home directory lives at:

```
/data/workbench/<your-slot>/home/
```

You cannot reach this path directly — it is your `~` inside JupyterLab.

### Shell history in the terminal

The workbench terminal uses **atuin** for shell history. atuin stores every
command you run — including the working directory, exit code, and duration —
and makes it searchable with <kbd>Ctrl</kbd>+<kbd>R</kbd>.

Because atuin's history database is inside your persistent home directory, your
history accumulates across every session. Commands you ran last week are
searchable today.

**Tips:**
- Press <kbd>Ctrl</kbd>+<kbd>R</kbd> in any terminal to open the interactive search.
- Type part of a command to filter. Use arrow keys to select, <kbd>Enter</kbd> to run.
- History is local to your slot — it is not shared with other users or synced anywhere.

### Uploading data

Use `abc data upload` from your local machine to push files to your MinIO bucket,
then reference the S3 path in your notebooks.

### Stopping your session

Your server is automatically stopped by the operator after a period of
inactivity (typically 2 hours). You can also stop it manually from the
JupyterHub control panel: click the **Hub Control Panel** link in the
JupyterLab File menu → **Stop My Server**.

Stopping does not delete any files.

---

## For operators

### Prerequisites

The workbench requires TLJH (The Littlest JupyterHub) with SystemdSpawner
and the abc-auth-svc forward-auth stack deployed on the platform node. See
the [seedling deployment guide](index) for the full installation record.

You need a JupyterHub admin service token in your `config.yaml`:

```bash
# On the platform node (aither), generate a token:
sudo tljh-config generate-admin-token

# Store it in your active abc context:
abc config set contexts.<name>.admin.services.workbench.hub_token <token>

# Optional — override the default hub URL:
abc config set contexts.<name>.admin.services.workbench.hub_url https://workbench.your-cluster.example.com
```

All `abc admin services workbench` commands read these two config keys.

### View active sessions

```bash
abc admin services workbench sessions
```

```
SLOT               STARTED           LAST ACTIVITY     IDLE
calm-dassie        2026-05-29 09:12  2026-05-29 11:03  1h22m
lunar-hornbill     2026-05-29 10:45  2026-05-29 11:28  45m
```

Shows every pool slot with a running JupyterLab server, when it started,
and how long it has been idle.

### Idle-session reaper

Stop servers that have been idle longer than a threshold. Users' files are
always preserved — only the server process is stopped.

```bash
# Run the reaper once (suitable for cron):
abc admin services workbench watch --once

# Run continuously, checking every 60 s (suitable for a systemd unit):
abc admin services workbench watch

# Dry-run — see what would be stopped without stopping anything:
abc admin services workbench watch --dry-run --once
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--idle-timeout` | `2h` | Stop servers idle longer than this |
| `--interval` | `60s` | Poll interval (ignored with `--once`) |
| `--dry-run` | false | Report without stopping |
| `--once` | false | One check then exit |
| `--node` | from config | SSH alias for the platform node |

**Running as a systemd unit** (recommended for production):

```ini
# /etc/systemd/system/abc-workbench-watch.service
[Unit]
Description=abc workbench idle-session reaper
After=network.target

[Service]
ExecStart=/usr/local/bin/abc admin services workbench watch
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now abc-workbench-watch
```

### Provisioning atuin for all slots

Run once after initial workbench deployment, and again whenever new slots
are added:

```bash
# Provision all slots (discovers /data/workbench/*/home/ automatically):
abc admin services workbench provision-atuin

# Provision specific slots only:
abc admin services workbench provision-atuin --slots calm-dassie,lunar-hornbill

# Dry-run first:
abc admin services workbench provision-atuin --dry-run
```

This command:

1. Checks whether `atuin` is installed at `/usr/local/bin/atuin` on the
   platform node. If not, downloads the pinned static binary from GitHub
   releases and installs it system-wide.
2. For each slot, appends an `eval "$(atuin init bash)"` stanza to
   `~/.bashrc` (idempotent — runs only once per slot).
3. Writes `~/.config/atuin/config.toml` with local-only settings
   (`auto_sync = false`, no sync server).

After this runs, every user who opens a terminal in JupyterLab gets atuin
automatically. No user action required.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--slots` | all | Comma-separated slot names to provision |
| `--atuin-version` | `v18.4.0` | atuin release to install if absent |
| `--dry-run` | false | Show what would happen without changing anything |
| `--node` | from config | SSH alias for the platform node |

### Pool slot layout

Each pool slot maps to a Linux system user on the platform node:

```
System user:   jupyter-<slot>          (e.g. jupyter-calm-dassie)
Home dir:      /data/workbench/<slot>/home/
Systemd unit:  jupyter-calm-dassie.service  (managed by TLJH + SystemdSpawner)
Atuin DB:      /data/workbench/<slot>/home/.local/share/atuin/history.db
```

Home directories are owned by `jupyter-<slot>:jupyter-<slot>` with mode
`750`. They are created during bulk provisioning and persist indefinitely —
they are never removed when a server stops.

### Troubleshooting

**User cannot log in**

Check that the pool slot exists in Supabase / the claim database and that
the MinIO credentials are valid. The forward-auth stack validates MinIO
credentials on every request — a wrong password returns 401.

**Server stuck starting (spinner)**

Check the SystemdSpawner log for the slot:

```bash
ssh sun-aither sudo journalctl -u jupyter-calm-dassie.service -n 50
```

**Kernel stuck at `[*]` / WebSocket errors**

Confirm the Caddy `@websocket` matcher is in place to bypass forward-auth
for WebSocket upgrade requests. See the
[Caddy / TLJH deployment record](https://workbench.seedling.abc-cluster.cloud)
for the exact config block.

**atuin not available in terminal**

Run `provision-atuin` for the affected slot:

```bash
abc admin services workbench provision-atuin --slots <slot-name>
```

Then ask the user to open a new terminal tab (existing tabs won't pick up
the updated `.bashrc`).
