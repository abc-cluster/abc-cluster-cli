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

### Connecting an external editor

The browser JupyterLab needs no token — your cluster login handles it. If you
prefer a desktop editor, mint a connection token from your laptop:

```bash
abc workbench connect --client vscode
```

This prints a **JupyterHub server URL**, your **username**, and a **token**
(valid 7 days by default). In **VS Code**:

1. Command Palette → **Jupyter: Specify Jupyter Server for Connections** →
   **Existing JupyterHub Server** (the one *with* "Hub").
2. **Server URL:** the hub root (e.g. `https://workbench.seedling.abc-cluster.cloud`)
   — not a `/user/...` URL.
3. **Username:** as printed.
4. **Password:** paste the **token** (an API token is accepted in the password field).

Then pick the JupyterHub server in the notebook's kernel selector.

:::note Why the "Hub" provider, and not "Existing Jupyter Server"
A JupyterHub token only authenticates as an HTTP header. VS Code's plain
"Existing Jupyter Server" provider puts the token in the URL query string, which
JupyterHub rejects — so it cannot connect. The "Existing **JupyterHub** Server"
provider sends the token as a header throughout and is the only one that works.
:::

**Positron desktop** has no remote-Jupyter provider yet
([posit-dev/positron#8300](https://github.com/posit-dev/positron/issues/8300)),
so a token cannot be used there. Use VS Code, the browser, or — once your
operator installs it — **Positron Server from the JupyterHub launcher** (browser
Positron that needs no token, via
[`jupyter-positron-server`](https://github.com/posit-dev/jupyter-positron-server)).

Revoke a token when you're done (from a workbench terminal):

```bash
abc workbench token revoke <token-name>
```

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

### Group common/shared mounts (geesefs)

Operators can expose a group's MinIO bucket subdirectories as FUSE mounts
inside every slot home directory. This is useful when a research group needs
a shared read-only data drop (`~/group-common/`) or a collaborative scratch
area (`~/group-shared/`).

```bash
# Mount for group mbhg-hostgen, all slots:
abc admin services workbench provision-group-mounts --group mbhg-hostgen

# Specific slots only:
abc admin services workbench provision-group-mounts --group mbhg-hostgen \
  --slots calm-dassie,lunar-hornbill

# Dry-run first:
abc admin services workbench provision-group-mounts --group mbhg-hostgen \
  --dry-run
```

After this runs, every slot in the group sees:

```
~/group-common/    read-only view of s3://su-<group>/common/
~/group-shared/    read-write view of s3://su-<group>/shared/
```

**How it works:**

1. Installs `fuse3` and enables `user_allow_other` on the platform node.
2. Downloads the pinned [geesefs](https://github.com/yandex-cloud/geesefs)
   static binary to `/usr/local/bin/geesefs` (if absent).
3. Creates `common/.keep` and `shared/.keep` placeholder objects in the
   bucket so the prefixes appear in listings.
4. Writes and starts one `abc-geesefs-<group>.service` systemd unit that
   FUSE-mounts the bucket at `/mnt/abc-group-<group>/` — streaming
   directly from MinIO. **Zero disk used on aither.**
5. For each slot: adds two fstab bind-mount entries and mounts immediately.

**Storage intent:** these directories are for casual copy operations only.
Do not use them as Nextflow work directories or Nomad job scratch — FUSE
latency degrades job throughput significantly.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--group` | required | Group name, e.g. `mbhg-hostgen` (strips leading `su-` automatically) |
| `--slots` | all | Comma-separated slot names |
| `--geesefs-version` | `v0.42.1` | geesefs release to install if absent |
| `--dry-run` | false | Show what would be done without changing anything |
| `--node` | from config | SSH alias for the platform node |

**Grove/garden upgrade path:** replace geesefs with JuiceFS backed by
Redis/PostgreSQL for full POSIX semantics across multiple compute nodes.

---

### Pool slot layout

Each pool slot maps to a Linux system user on the platform node:

```
System user:   jupyter-<slot>          (e.g. jupyter-calm-dassie)
Home dir:      /data/workbench/<slot>/home/
Systemd unit:  jupyter-calm-dassie.service  (managed by TLJH + SystemdSpawner)
Atuin DB:      /data/workbench/<slot>/home/.local/share/atuin/history.db
Group mounts:  ~/group-common/ (ro)  ~/group-shared/ (rw)  — if provisioned
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

**group-common / group-shared not appearing**

Check whether the geesefs service is running:

```bash
ssh sun-aither sudo systemctl status abc-geesefs-<group>.service
```

If stopped, check the journal for errors (typically a credential or endpoint
misconfiguration):

```bash
ssh sun-aither sudo journalctl -u abc-geesefs-<group>.service -n 50
```

Re-run `provision-group-mounts` after fixing credentials:

```bash
abc admin services workbench provision-group-mounts --group <group>
```
