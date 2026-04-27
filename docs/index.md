---
sidebar_position: 1
slug: /
---

# abc CLI

`abc` is the command-line interface for the **African Bioinformatics Computing Cluster**. It covers the full workflow: configure contexts, protect credentials, move data, submit jobs, and manage the cluster fabric.

## What's here

| Section | What you'll find |
|---|---|
| [Installation](./installation) | Install on Linux, macOS, or Windows |
| [Quick start](./quickstart) | First commands in under five minutes |
| [Tutorials](./tutorials) | Hands-on walkthrough of every major feature |
| [Reference](./reference) | Every command, flag, and environment variable |

## The three-sentence version

Set an **active context** (`abc context add`) that points `abc` at your cluster endpoint and tokens. Researchers use `abc data upload/download`, `abc job run`, and `abc pipeline run`. Operators use `abc admin services …` to proxy into Nomad, Vault, Consul, and Tailscale CLIs without manual token wrangling.
