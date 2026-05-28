---
sidebar_position: 5
title: Interacting with the cluster
---

<!--
  Synced from ../../DEMO.md
  Single source of truth: analysis/packages/abc-cluster-cli/DEMO.md
  Re-sync with: cp ../../DEMO.md tutorials/demo.md && prepend frontmatter
  Note: the sidebar label is driven by the frontmatter `title` field;
  the H1 below is kept as the long-form page heading.
-->

# Interacting with the cluster

A focused walkthrough of the **ABC CLI** built around the three things researchers do every day.

| Part | What you will practise |
|------|------------------------|
| [Setup](#setup-5-min) | Install, claim a code, preflight, sync capabilities |
| [1 — Jobs](#part-1-submit-and-monitor-jobs-15-min) | Submit jobs, watch them run, read logs |
| [2 — Upload & encrypt](#part-2-upload-and-encrypt-data-10-min) | Push data to the cluster, encrypt sensitive files |
| [3 — Browse](#part-3-browse-data-10-min) | List buckets, inspect objects, stat a key |

**Time budget:** roughly **40 minutes** end to end.

**Cluster:** All exercises target your configured abc-cluster (for the seedling beta, `https://nomad.seedling.abc-cluster.cloud`). Your workspace lead provides a pre-configured `~/.abc/config.yaml` — you do not need to create credentials from scratch.

**Deeper reference:** `abc <command> --help` is always accurate. The [reference docs](../reference/) have every flag and preamble directive.

---

## Setup (~5 min)

### Install

`abc` is a single static binary. Quick install:

```bash
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \
  | sh -s --
```

Verify:

```bash
abc --version
```

### Get your credentials

The fastest path is a **claim code** from your workshop facilitator —
one command installs a ready-to-use config and makes it active:

```bash
abc auth claim <CLAIM_CODE> --email you@sun.ac.za --name "Your Name"
```

You'll be asked to accept POPIA consent; on success the CLI writes
`~/.abc/config.yaml` and activates the new context. (See
[Reference → auth](../reference/auth) for the blind-pool and stdin forms.)

If your lead handed you a config YAML file directly instead of a code:

```bash
abc config init                              # if ~/.abc doesn't exist yet
abc auth context add --from-file ~/Downloads/<your-name>.yaml
```

Either way, confirm the active context and your identity:

```bash
abc auth context show
abc auth whoami
```

`abc auth whoami` contacts the Nomad API to resolve your token name and saves it to `auth.whoami` in the active context for future reference.

### Preflight with `abc doctor`

Before pulling cluster details or submitting work, confirm the CLI can reach **and run work on** the cluster. `abc doctor` checks your config, connectivity, and submits a tiny probe job end to end:

```bash
abc doctor

# Config + connectivity only (skip the probe job):
abc doctor --skip-job
```

Exit code `0` means you're good to go. If a check fails, the output tells you which group (config / connectivity / workload) and why — fix that first. See [Reference → doctor](../reference/doctor) for the full check list.

### Sync cluster capabilities

Pull your storage credentials, node inventory, and driver list into the active context:

```bash
abc cluster capabilities sync
```

This populates the S3 endpoint that `abc data ls` needs and the driver list `abc job run` uses for placement. Re-run it any time the cluster's capabilities change.

---

## Part 1: Submit and monitor jobs (~15 min)

`abc job run` turns an annotated shell script into a Nomad batch job and submits it. You do not need to write an HCL file or know how Nomad works.

### 1.1 Submit the built-in workload

One workload is baked into the CLI — no script file required. It runs a randomised **stress-ng** job across CPU, VM, and I/O stressors, which is useful for verifying your workspace quota and confirming the cluster can schedule onto your namespace:

```bash
abc job run hello-cluster
```

The CLI prints the job ID. Note it for the next steps.

### 1.2 Monitor the job

List all your running jobs:

```bash
abc job list --status running
```

Show details for a specific job:

```bash
abc job show <job-id>
```

Stream live logs:

```bash
abc job logs <job-id> --follow
```

Stop a job if needed:

```bash
abc job stop <job-id>
```

### 1.3 Debug: open a shell before work starts

Add a pre-work sleep so you have time to exec into the allocation before the workload begins:

```bash
abc job run hello-cluster --sleep=2m
```

`--sleep` accepts plain seconds (`120`), Go duration strings (`2m`, `1h30m`), or `HH:MM:SS` walltime format.

### 1.4 Submit a custom script

Replace `Your Name` below and submit a personalised job:

```bash
cat > hello-me.sh << 'EOF'
#!/bin/bash
#ABC --cores=1
#ABC --mem=256M
echo "Hello, Your Name!"
echo "Running on: $(hostname)"
echo "Alloc: ${NOMAD_ALLOC_ID}"
EOF

abc job run hello-me.sh
```

The CLI marks the script executable automatically. `#ABC` preamble lines set default resources; CLI flags override them:

```bash
abc job run hello-me.sh --cores=2 --mem=512M
```

### 1.5 Attach a software environment

The CLI ships three software-stack runtimes, all wired through the same `--runtime` + `--from-file` pair. Pick the one that matches how your project pins dependencies today.

#### Option A — Pixi (`--runtime=pixi-exec`)

Best if you already have (or want) a Pixi project with a lockfile for reproducibility.

```bash
mkdir bio-pixi && cd bio-pixi
pixi init
pixi add --feature bio --platform linux-64 samtools fastqc
pixi install --locked --feature bio          # writes pixi.lock

cat > bio-pixi.sh << 'EOF'
#!/bin/bash
#ABC --name=bio-pixi-demo
#ABC --runtime=pixi-exec
#ABC --from-file=pixi.lock
#ABC --driver=containerd
#ABC --driver.config.image=docker.io/library/debian:12-slim
#ABC --cores=4
#ABC --mem=8G
set -euo pipefail
samtools --version | head -1
fastqc --version
EOF

abc job run bio-pixi.sh
```

`pixi.lock` (vs `pixi.toml`) gives bit-for-bit reproducibility — the wrapper invokes `pixi install --locked`. `debian:12-slim` is just a base rootfs; Pixi brings in everything the script needs.

#### Option B — Micromamba (`--runtime=micromamba-exec`)

Best if you have an `environment.yml` from a colleague or a bioconda recipe and don't want to add a new tool to your workflow.

```bash
cat > environment.yml << 'EOF'
name: bio
channels:
  - conda-forge
  - bioconda
dependencies:
  - samtools=1.20
  - fastqc=0.12.1
EOF

cat > bio-mamba.sh << 'EOF'
#!/bin/bash
#ABC --name=bio-mamba-demo
#ABC --runtime=micromamba-exec
#ABC --from-file=environment.yml
#ABC --driver=containerd
#ABC --driver.config.image=docker.io/library/debian:12-slim
#ABC --cores=4
#ABC --mem=8G
#ABC --mamba-cleanup
set -euo pipefail
samtools --version | head -1
fastqc --version
EOF

abc job run bio-mamba.sh
```

`#ABC --mamba-cleanup` removes the per-task conda environment when the script ends — sensible for one-shot jobs; omit for iterative runs that benefit from caching.

> **Note on `--from-file`** — this is the canonical flag name across the CLI (same as `abc auth context add --from-file`). The older `--from` is still accepted but emits a deprecation nudge.

---

## Part 2: Upload and encrypt data (~10 min)

`abc data upload` uses the **TUS resumable upload protocol** — uploads survive network interruptions and can be resumed exactly where they left off.

### 2.1 Upload a file

```bash
abc data upload ./results.tar.gz \
  --meta researcher=alice \
  --meta project=my-study
```

`--meta` attaches arbitrary key-value metadata to the object. The upload endpoint and token resolve automatically from the active context.

### 2.2 Upload with encryption

Sensitive files should be encrypted before leaving your machine. `abc data` uses an **age** key pair managed by `abc secrets`:

```bash
# One-time setup: generate your key pair
abc secrets init
```

Then encrypt on upload:

```bash
abc data upload ./patient-data.tar.gz --encrypt
```

The file is encrypted locally with your age public key before any bytes are transmitted. Nobody on the cluster can read the ciphertext without the matching private key.

### 2.3 Encrypt or decrypt locally (without uploading)

Encrypt a local file:

```bash
abc data encrypt sensitive-report.pdf   # → sensitive-report.pdf.enc
```

Decrypt it back:

```bash
abc data decrypt sensitive-report.pdf.enc   # → sensitive-report.pdf
```

This is useful for encrypting files before sharing them via other channels, or for decrypting files that a collaborator encrypted with your public key.

### 2.4 Download from a public source to cluster storage

`abc data download` runs the transfer **on the cluster** as a Nomad job — the data never passes through your laptop. Tool binaries are auto-staged from the cluster's binary mirror, so no per-node install is needed:

```bash
abc data download \
  --tool s5cmd \
  --tool-args '--no-sign-request' \
  --source 's3://sra-pub-src-10/SRR19090886/*.fastq.gz' \
  --destination storage \
  --driver exec
```

`--destination storage` writes files to your research-group bucket under `s3://<namespace>/downloads/<your-identity>/`. Supported tools: `wget`, `aria2`, `rclone`, `s5cmd`.

---

## Part 3: Browse data (~10 min)

`abc data ls` lists buckets and objects in the cluster's S3-compatible storage (MinIO / RustFS). Credentials resolve from the active context.

### 3.1 List all buckets

```bash
abc data ls
```

### 3.2 List objects in a bucket

```bash
abc data ls <your-namespace>
```

Drill into a prefix:

```bash
abc data ls <your-namespace>/downloads/
```

Long format shows size and last-modified timestamp:

```bash
abc data ls <your-namespace>/downloads/ --long
```

### 3.3 Inspect an object

`abc data stat` prints size, ETag, last-modified date, and any user metadata attached at upload time:

```bash
abc data stat <your-namespace>/downloads/alice/results.tar.gz
```

The `researcher` and `project` metadata you passed with `--meta` in Part 2 appears here — useful for tracing provenance without downloading the file.

---

## Quick-reference card

| Task | Command |
|------|---------|
| Confirm identity | `abc auth whoami` |
| Submit built-in workload | `abc job run hello-cluster` |
| Submit custom script | `abc job run <script.sh>` |
| Override resources at submit | `abc job run <script> --cores=4 --mem=16G` |
| List running jobs | `abc job list --status running` |
| Show job details | `abc job show <job-id>` |
| Stream logs | `abc job logs <job-id> --follow` |
| Stop a job | `abc job stop <job-id>` |
| Upload a file | `abc data upload <file> [--meta k=v …]` |
| Upload and encrypt | `abc data upload <file> --encrypt` |
| Encrypt locally | `abc data encrypt <file>` |
| Decrypt locally | `abc data decrypt <file>.enc` |
| List buckets | `abc data ls` |
| List objects | `abc data ls <bucket>[/prefix] [--long]` |
| Inspect an object | `abc data stat <bucket>/<key>` |

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Anything not working | `abc doctor` — runs config + connectivity + a probe job and tells you which layer failed |
| `connect: connection refused` | You need to be on the Stellenbosch network or Tailscale VPN |
| `403 Forbidden` | `abc auth context show` — confirm the **seedling** context is active and your token is set |
| Job goes to wrong namespace | `abc auth context show` — check the `nomad_namespace` field |
| `abc data ls` returns no endpoint | Run `abc cluster capabilities sync` to pull storage credentials |
| `unknown command` | `abc --help` for the command list; `abc <command> --help` for flags |

---

## Next steps

- **[Reference → job run](../reference/jobs)** — full `#ABC` directive table, runtimes, and all flags.
- **[Reference → data](../reference/data)** — upload, download, encrypt, and object storage.
- **[`USAGE.md`](https://github.com/abc-cluster/abc-cluster-cli/blob/main/USAGE.md)** — every command, flag, and environment variable.
