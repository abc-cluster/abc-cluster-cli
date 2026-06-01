---
sidebar_position: 1
---

# abc CLI

`abc` is the command-line interface for the **African Bioinformatics Computing Cluster**. It covers the full workflow: configure contexts, protect credentials, move data, submit jobs, and manage the cluster fabric.

## What's here

| Section | What you'll find |
|---|---|
| [Quick start](./quickstart) | First commands in under five minutes |
| [Tutorials](./tutorials) | Hands-on walkthrough of every major feature |
| [Reference](./reference) | Every command, flag, and environment variable |

## The three-sentence version

Set an **active context** (`abc context add` or paste a per-user `~/.abc/config.yaml` from your workspace lead) that points `abc` at your cluster endpoint and tokens. Researchers use `abc data upload/download`, `abc job run`, and `abc pipeline run`. Operators use `abc admin services …` to proxy into Nomad, Vault, Consul, and Tailscale CLIs without manual token wrangling.

## Install

`abc` ships as a single static binary for Linux, macOS, and Windows (amd64 + arm64).

### One-liner install (recommended)

**System-wide** — installs to `/usr/local/bin/abc`:

```bash
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \
  | sh -s -- --sudo
```

**User-local** — installs to `~/bin/abc` (no sudo):

```bash
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \
  | sh -s --
```

**Pin a specific release:**

```bash
# Replace v1.2.3 with the version you want
curl -fsSL ... | sh -s -- --version v1.2.3
```

After install, verify:

```bash
abc --version
```

### Updating

Once `abc` is installed, upgrade in place — no need to re-run the install script:

```bash
abc self-update            # upgrade to the latest release
abc self-update --check    # report current vs latest, download nothing
abc self-update --version v1.2.3   # install a specific tag (up- or downgrade)
abc self-update --yes      # skip the confirmation prompt
```

`self-update` detects your platform, downloads the matching release binary, and
swaps the running `abc` atomically. If `abc` lives in a root-owned directory
(e.g. `/usr/local/bin`), the final move is retried with `sudo` when you run it
interactively, or it prints the exact `sudo mv` command for you to run.

When a newer version exists, `abc` prints a one-line reminder on stderr:

```
[abc] update available: v1.2.4 (current v1.2.3)
[abc] upgrade with:  abc self-update
```

Silence that check with `export ABC_CLI_DISABLE_UPDATE_CHECK=1`.

> On Windows, a running `.exe` can't replace itself; `abc self-update` prints the
> download URL to overwrite manually.

### Other methods

#### `go install`

```bash
go install github.com/abc-cluster/abc-cluster-cli@latest
```

#### Build from source

```bash
git clone https://github.com/abc-cluster/abc-cluster-cli
cd abc-cluster-cli
go build -o abc .
```

### Shell completion

```bash
# bash
abc completion bash > /etc/bash_completion.d/abc

# zsh
abc completion zsh > "${fpath[1]}/_abc"

# fish
abc completion fish > ~/.config/fish/completions/abc.fish
```

Once `abc` is on your `$PATH`, head to the [Quick start](./quickstart) for the first-run flow.
