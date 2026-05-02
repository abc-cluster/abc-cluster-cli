---
sidebar_position: 10
---

# admin tools

Manage the cluster binary store: download, push, and update tool binaries used by Nomad jobs and Wave layers.

## Directory layout

```
~/.abc/assets/          Distribution artifacts: arch-suffixed binaries, JARs, tools.toml.
                        Everything here is pushed to cluster S3.
~/.abc/binaries/        Host executables: eget, mc, nomad, … Safe to add to $PATH.
                        Not pushed.
```

Remote path pattern: `<endpoint>/<bucket>/<prefix>/<tool>-<os>-<arch>`

## Commands

### `abc admin tools init`

Write `~/.abc/assets/tools.toml` from the bundled default. Run once after installing the CLI.

```bash
abc admin tools init           # create (no-op if already exists)
abc admin tools init --reset   # overwrite with the bundled default
abc admin tools init --show    # print the bundled default without writing
```

### `abc admin tools edit`

Open `~/.abc/assets/tools.toml` in `$EDITOR`.

```bash
abc admin tools edit
```

### `abc admin tools fetch`

Download all tool binaries for all configured architectures into `~/.abc/assets/`.

```bash
abc admin tools fetch                  # all tools, all arches
abc admin tools fetch --tool s5cmd     # single tool
abc admin tools fetch --arch amd64     # single arch
```

Target architectures are read from `admin.tools.architectures` in the active context (default: `linux/amd64`, `linux/arm64`).

### `abc admin tools push`

Upload cached binaries, `tools.toml`, and Wave layer archives to cluster S3.

```bash
abc admin tools push                   # push everything
abc admin tools push --tool s5cmd      # push one tool
abc admin tools push --dry-run         # preview without uploading
```

**What gets pushed:**

1. All arch-suffixed binaries in `~/.abc/assets/`
2. `tools.toml` as `<prefix>/tools.toml` (version reference for the cluster job that rebuilds wave layers)
3. Wave layer archives for every tool with `wave_inject = true`:

| Archive | Contents |
|---|---|
| `wave-layer-linux-<arch>.tar.gz` | All `wave_inject` tools combined |
| `wave-layer-linux-<arch>-<tool>.tar.gz` | Single tool |

These archives are used by `--wave-inject-tools` at job submit time (see [`abc job run`](./jobs.md#wave-tool-injection)).

### `abc admin tools list`

Show the local cache alongside remote state.

```bash
abc admin tools list
abc admin tools list --tool s5cmd
```

### `abc admin tools status`

Quick health check. Exits 1 if any configured binary is missing locally or remotely.

```bash
abc admin tools status
```

Useful in CI to verify the binary store is complete before submitting jobs.

### `abc admin tools artifact-url`

Print the Nomad `artifact` stanza URL for a tool. Useful when writing custom Nomad job HCL.

```bash
abc admin tools artifact-url s5cmd
# → http://<endpoint>/abc-reserved/binary_tools/s5cmd-linux-${attr.cpu.arch}
```

### `abc admin tools wave-layer`

Build and push Wave layer archives to cluster S3 by submitting a one-off Nomad batch job. Useful when the binary cache was updated remotely (e.g. a new tool version was fetched on the cluster) and you want to rebuild layers without a full local push.

```bash
abc admin tools wave-layer                   # amd64 + arm64
abc admin tools wave-layer amd64             # single arch
abc admin tools wave-layer amd64 --wait      # block until the job completes
abc admin tools wave-layer --tools s5cmd     # rebuild only the s5cmd layer
abc admin tools wave-layer --dry-run         # print generated HCL without submitting
```

The submitted Nomad job:
1. Fetches `s5cmd` from cluster S3 via an `artifact` stanza (public-read, no credentials)
2. Downloads `tools.toml` from `<endpoint>/abc-reserved/binary_tools/tools.toml`
3. Downloads each `wave_inject` binary for the target arch
4. Packs them as `usr/local/bin/<tool>` in a `.tar.gz`
5. Uploads both the combined and per-tool layer archives to `s3://abc-reserved/wave_lite_layers/`

Credentials for the S3 upload are read from the `nomad/jobs/abc-nodes-storage` Nomad Variable (set once with `abc admin services nomad cli -- var put ...`).

## `tools.toml` format

```toml
# ── Fetch engine ───────────────────────────────────────────────────────────
[engine]
repo    = "zyedidia/eget"
version = "v1.3.3"

# ── Push settings ──────────────────────────────────────────────────────────
[push]
bucket = "abc-reserved"
prefix = "binary_tools"

# ── Tools ──────────────────────────────────────────────────────────────────
[tools.s5cmd]
repo        = "peak/s5cmd"
version     = "v2.3.0"
wave_inject = true        # include in wave layer archives

[tools.rclone]
repo        = "rclone/rclone"
version     = "v1.73.5"
wave_inject = true

[tools.pixi]
repo    = "prefix-dev/pixi"
version = "v0.67.2"

[tools.micromamba]
repo    = "mamba-org/micromamba-releases"
version = "2.6.0-0"

[tools.wave]
repo    = "seqeralabs/wave-cli"
version = "v1.8.2"

[tools.restic]
repo     = "restic/restic"
version  = "latest"
disabled = true           # skip without removing the block

# ── Locally built artifacts ────────────────────────────────────────────────
[local.abc-node-probe]
paths."linux-amd64" = "/path/to/abc-node-probe-linux-amd64"
paths."linux-arm64" = "/path/to/abc-node-probe-linux-arm64"
```

### `wave_inject` field

Set `wave_inject = true` on any tool to include it in the Wave layer archives generated by `abc admin tools push`. The tool binary will be placed at `usr/local/bin/<name>` inside the archive.

After editing `tools.toml`, run `abc admin tools push` to rebuild and upload the new layer archives.

## Configuration

Target architectures and the push endpoint live in the active context:

```bash
# Set which arches to fetch/push
abc config set contexts.<name>.admin.tools.architectures '["linux/amd64","linux/arm64"]'

# Set the push endpoint (written automatically by abc admin tools push)
abc config set contexts.<name>.admin.tools.endpoint http://rustfs.aither

# Set the Wave Lite URL (required for --wave-inject-tools in abc job run)
abc config set contexts.<name>.admin.services.wave.http http://100.126.253.95:9090
```

## See also

- [`abc job run` — Wave tool injection](./jobs.md#wave-tool-injection) — use injected tools in batch jobs
- [`abc pipeline run`](./pipeline.md) — Nextflow pipelines with Wave container augmentation
