---
sidebar_position: 1
slug: /
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
