---
sidebar_position: 11
---

# workbench

Start, stop, and inspect interactive IDE sessions (VS Code / code-server and
Jupyter) running on the ABC cluster.

## Basic usage

```bash
abc workbench start           # Docker backend (default)
abc workbench start --backend=vm   # Multipass VM backend
abc workbench stop
abc workbench status
abc workbench url             # Print URL + password for the running session
abc workbench logs            # Stream IDE logs
```

## ⚠ Current limitation — admin access required

**The workbench is not yet self-service for pool users.**

Both backends (Docker and VM) register a path-based routing rule in the
platform Caddy via its admin API. That API is only reachable from the platform
node itself (`localhost:2019`), and `abc workbench start` reaches it by
SSHing to the platform node and running `curl` locally.

Pool users (`su-<group>-*` tokens, e.g. from `su-mbhg-hostgen`) do not have
SSH access to the platform node. Running `abc workbench start` from their
machine will fail at the route-registration step — the session may start in
Nomad but the public URL will not be reachable.

**Workaround until a route-registration service is available:**
An admin with SSH access to the platform node runs `abc workbench start` with
the pool user's context active:

```bash
# On the admin machine, which has SSH to sun-aither:
abc context use <pool-user-context>
abc workbench start --backend=vm
# Prints: https://workbench.seedling.abc-cluster.cloud/<user>/
# Admin shares the URL + password with the user.
```

The user then accesses the session in their browser — no SSH or Tailscale
required once the route is registered.

This restriction will be lifted when a Nomad-integrated route-registration
service lands (tracked in `brainstorms/nomad-driver-multipass/`).

---

## Subcommands

### `abc workbench start`

Start a workbench session. Prints the HTTPS URL and password when ready.

```bash
abc workbench start [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--backend` | `docker` | Backend: `docker` (Nomad service job) or `vm` (Multipass VM, suspend/resume) |
| `--cores N` | `2` | CPU cores allocated to the session |
| `--mem N` | `4096` | Memory in MB |
| `--idle-hours N` | `4` | Auto-stop after N hours of idle (0 = no timeout) |
| `--ide NAME` | `quarto` | IDE image: `quarto` (abc-quarto-base) or `code-server` |
| `--project PATH` | — | Open this directory in the IDE on startup |
| `--node NAME` | platform node | Pin session to a specific Nomad node |
| `--no-telemetry` | — | Disable session-watcher sidecar |

**Docker backend** (`--backend=docker`): submits a Nomad service job in the
user's namespace. The session runs as a container. Fast to start; destroyed
on stop (homedir bind-mount persists the home directory on the node).

**VM backend** (`--backend=vm`): provisions a Multipass VM on the platform
node (first time: ~5 min; subsequent resume: ~8 s). The VM is suspended on
stop rather than destroyed — tool installations and home-directory state
survive across sessions. The VM name is `wb-<user>` (underscores replaced
with hyphens for Multipass compatibility).

### `abc workbench stop`

Stop the running session. For the VM backend, the VM is suspended (state
preserved); for the Docker backend, the Nomad job is deregistered. The
path-based Caddy route is removed in both cases so the URL returns 404
while the session is not running.

```bash
abc workbench stop
```

### `abc workbench status`

Show the current session status, backend, URL, and resource allocation.

```bash
abc workbench status
```

### `abc workbench url`

Print the IDE URL and password for the running session. Useful after closing
the terminal that ran `abc workbench start`.

```bash
abc workbench url
```

Output includes:
- `Browser` — the public HTTPS path-based URL
- `Direct` — the raw `host:port` (Tailscale / platform network only)
- `Password` — the code-server password (stable, derived from MinIO key)
- `Remote SSH` — the SSH alias for VS Code / Positron Remote SSH (VM backend only)

### `abc workbench logs`

Stream logs from the running session.

```bash
abc workbench logs
```

---

## Session URL model

Every session is accessible at:

```
https://workbench.seedling.abc-cluster.cloud/<user>/
```

The path prefix `/<user>/` is routed by a Caddy instance on the platform
node. Routing is registered dynamically by `abc workbench start` and removed
by `abc workbench stop`. If the session is not running, the URL returns 404.

The URL is stable: the same address across all sessions for a given user,
regardless of which VM IP or dynamic port the session is assigned.

---

## Persistent storage

| Location | What it holds | Survives stop? |
|---|---|---|
| `~/` inside the session | Installed tools, notebooks, dotfiles | ✅ (bind-mounted from `/data/workbench/<user>/home/` on the node) |
| `s3://<user>/` | Pipeline outputs, uploaded data | ✅ (MinIO, independent of workbench) |

The home directory (`~/`) is stored at `/data/workbench/<user>/home/` on the
platform node. It is bind-mounted into every session for the same user, so
tools installed in one session are available in the next.

:::note
The home directory must be created before the first session. Admins create it
when provisioning a user slot:
```bash
ssh sun-aither "mkdir -p /data/workbench/<user>/home"
```
:::

---

## Remote SSH (VM backend)

After `abc workbench start --backend=vm`, a `Host wb-<user>` block is written
to `~/.ssh/config` on the local machine:

```
Host wb-<user>
    HostName <vm-ip>
    User ubuntu
    ProxyJump sun-aither
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
```

Connect from VS Code or Positron: **Remote SSH → `wb-<user>`**.

The SSH alias is updated automatically if the VM gets a new IP (e.g. after
a VM recreate). Run `abc workbench url` to see the current SSH config block.
