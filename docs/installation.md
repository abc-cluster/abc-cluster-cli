---
sidebar_position: 2
---

# Installation

`abc` ships as a single static binary for Linux, macOS, and Windows (amd64 + arm64).

## One-liner install (recommended)

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

## Other methods

### go install

```bash
go install github.com/abc-cluster/abc-cluster-cli@latest
```

### Build from source

```bash
git clone https://github.com/abc-cluster/abc-cluster-cli
cd abc-cluster-cli
go build -o abc .
```

## Shell completion

```bash
# bash
abc completion bash > /etc/bash_completion.d/abc

# zsh
abc completion zsh > "${fpath[1]}/_abc"

# fish
abc completion fish > ~/.config/fish/completions/abc.fish
```
