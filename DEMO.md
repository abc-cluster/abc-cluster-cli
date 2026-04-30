# ABC CLI Hands-On Walkthrough

A focused walkthrough of the **ABC CLI** covering the commands you will use every day. Each exercise takes about **ten minutes**; skip or skim anything you have already seen.

## Overview

The ABC CLI turns annotated shell scripts into **Nomad batch jobs** (`abc job run`), moves data to and from the cluster (`abc data`), and dispatches higher-level Nextflow workflows (`abc pipeline run`, `abc module run`). A small `abc context` / `abc auth` layer tells the CLI which cluster to talk to.

| Exercise | Focus |
|----------|--------|
| [1](#exercise-1-context-setup) | Context setup and identity |
| [2](#exercise-2-running-jobs) | `abc job run`: built-in workload and custom scripts |
| [3](#exercise-3-tracking-jobs) | `abc job show`, logs, list, stop |
| [4](#exercise-4-data-upload-and-download) | `abc data upload`, `download` |
| [5](#exercise-5-pipelines-and-modules) | `abc pipeline run`, `abc module run` |

**Time budget:** about **10 minutes per exercise** — 50 minutes for a complete pass.

**Cluster:** All exercises target the **aither** cluster (`http://aither.mb.sun.ac.za`). Your workspace lead will hand you a pre-configured `~/.abc/config.yaml` — you do not need to create credentials from scratch.

**Deeper reference:** [`USAGE.md`](USAGE.md) has every flag and preamble directive. `abc <command> --help` is always accurate.

---

## Prerequisites

- **`abc` on your `PATH`:** confirm with `abc --version`.
- Config file at `~/.abc/config.yaml` — handed to you by your workspace lead.

---

## Exercise 1: Context setup

**Time target:** about 10 minutes.

Bootstrap the config directory if it doesn't exist yet:

```bash
abc config init          # creates ~/.abc/config.yaml with a placeholder context
```

Replace the placeholder with the YAML your workspace lead gave you:

```bash
cp ~/Downloads/<your-name>.yaml ~/.abc/config.yaml
```

Activate the **aither** context and confirm it is the active one:

```bash
abc context use aither
abc context show
```

Confirm your identity against the cluster:

```bash
abc auth whoami
```

This contacts the Nomad API to resolve your token name and saves it to `auth.whoami` in the active context for future reference.

---

## Exercise 2: Running jobs

**Time target:** about 10 minutes.

`abc job run` converts an annotated shell script into a Nomad batch job and submits it.

### 2.1 Built-in verification workload

One workload is baked into the CLI — no script file required. It runs a randomised **stress-ng** job across CPU, VM, and I/O stressors, which is useful for verifying your workspace quota and checking that the cluster can schedule on your namespace:

```bash
abc job run hello-abc
```

### 2.2 Debug sleep: exec into a running allocation

Add a sleep at the start of any job so you have time to open a shell inside the allocation before the workload begins:

```bash
abc job run hello-abc --sleep=120s
```

`--sleep` accepts plain seconds (`120`), Go duration strings (`2m`, `1h30m`), or `HH:MM:SS` walltime format.

### 2.3 Your first custom job

Replace `Your Name` with your own name and submit a personalised job:

```bash
cat > hello-me.sh << 'EOF'
#!/bin/bash
#ABC --cores=1
#ABC --mem=256M
echo "Hello, Your Name!"
echo "Running on: $(hostname)"
EOF
```

Submit when ready (the CLI marks the script executable automatically):

```bash
abc job run hello-me.sh
```

### 2.4 Override directives from the CLI

CLI flags take precedence over `#ABC` preamble lines — useful for quick resource adjustments without editing the script:

```bash
abc job run hello-me.sh --cores=2 --mem=512M
```

### 2.5 Optional: Pixi software stack

If the cluster has [Pixi](https://pixi.sh) available, add a runtime stack to any job:

```bash
cat > pixi-job.sh << 'EOF'
#!/bin/bash
#ABC --name=pixi-demo
#ABC --driver=exec
#ABC --runtime=pixi-exec
#ABC --from=/cluster/envs/myproject/pixi.toml
set -euo pipefail
python -c "import pandas; print(pandas.__version__)"
EOF
abc job run pixi-job.sh
```

`--runtime=pixi-exec` (alias `pixi`) wraps your script in a `pixi run` invocation using the manifest at `--from`.

---

## Exercise 3: Tracking jobs

**Time target:** about 10 minutes.

### 3.1 List recent jobs

```bash
abc job list
abc job list --status running
abc job list --status dead --limit 5
```

### 3.2 Show job details

```bash
abc job show <job-id>
```

This shows resource allocation, meta keys (including the `random_scenario` set by `hello-abc`), and allocation status.

### 3.3 Stop a job

```bash
abc job stop <job-id>
```

---

## Exercise 4: Data upload and download

**Time target:** about 10 minutes.

### 4.1 Upload a file

`abc data upload` uses the **TUS resumable upload protocol** — uploads can be paused and resumed on flaky networks:

```bash
abc data upload ./results.tar.gz \
  --meta researcher=alice \
  --meta project=my-study
```

The upload endpoint and token resolve from the active context (`upload_endpoint` / `upload_token`).

### 4.2 Download SRA data to cluster storage

`abc data download` runs the transfer **on the cluster** as a Nomad job. The `exec` driver runs on the host filesystem and has direct internet access; tool binaries (`s5cmd`, etc.) are auto-staged via the Nomad artifact stanza from the cluster's `abc-reserved/binary_tools/` mirror, so no per-node install is needed.

Use `--tool s5cmd` with `--tool-args '--no-sign-request'` to fetch from SRA's public S3 bucket (no credentials required):

```bash
abc data download \
  --tool s5cmd \
  --tool-args '--no-sign-request' \
  --source 's3://sra-pub-src-10/SRR19090886/*.fastq.gz' \
  --driver exec
```

Without `--destination`, files land in `/tmp/abc-data-download` on the Nomad allocation's local scratch — accessible during the job's lifetime.

To persist them to your workspace's S3 bucket on the cluster, add `--destination storage`. The CLI resolves your rustfs/MinIO endpoint and credentials from the active context and stores the files under `s3://<your-namespace>/downloads/<your-identity>/` — i.e., a `downloads/<user>/` prefix inside your research-group bucket. The `group-member` IAM policy grants R/W on that prefix via `${aws:username}`, so every workspace member writes to their own folder without any extra setup:

```bash
abc data download \
  --tool s5cmd \
  --tool-args '--no-sign-request' \
  --source 's3://sra-pub-src-10/SRR19090886/*.fastq.gz' \
  --destination storage \
  --driver exec
```

For HTTPS sources (e.g. NCBI, ENA), use `--tool wget` or `--tool aria2` instead — both also resolve `--destination storage` to the same `<namespace>/downloads/<user>/` location.

`--node` pins the job to a specific Nomad node by name or ID. Supported tools: `wget`, `aria2`, `rclone`, `s5cmd`.

---

## Exercise 5: Pipelines and modules

**Time target:** about 10 minutes.

### 5.1 Run the Nextflow demo pipeline

`abc pipeline run` submits a Nextflow head job to the cluster:

```bash
abc pipeline run nextflow-io/hello
```

Check that the job was submitted:

```bash
abc job list --status running
```

### 5.2 Run a module with your data

`abc module run` generates and submits a Nextflow module-driver pipeline. You need a samplesheet CSV pointing at your input files. If you ran the download in Exercise 4 with `--destination storage`, your files are at `s3://<your-namespace>/downloads/<your-identity>/`. Create `samplesheet.csv`:

```
sample,fastq_1,fastq_2,strandedness
SRR19090886,s3://<namespace>/downloads/<your-identity>/SRR19090886_1.fastq.gz,s3://<namespace>/downloads/<your-identity>/SRR19090886_2.fastq.gz,auto
```

Save this as `samplesheet.csv`, then run FastQC on your files:

```bash
abc module run nf-core/fastqc \
  --samplesheet ./samplesheet.csv
```

Alternatively, use the seqkit stats module for a quicker sequence summary:

```bash
abc module run nf-core/seqkit/stats \
  --samplesheet ./samplesheet.csv
```

---

## Key takeaways

| Task | Command |
|------|---------|
| Bootstrap config | `abc config init` |
| Switch to aither context | `abc context use aither` |
| Show identity | `abc auth whoami` |
| Submit built-in workload | `abc job run hello-abc` |
| Debug interactively | `abc job run <script> --sleep=2m` |
| Submit custom script | `abc job run hello-me.sh` |
| List jobs | `abc job list --status running` |
| Show job details | `abc job show <job-id>` |
| Stop a job | `abc job stop <job-id>` |
| Upload file | `abc data upload <file> [--meta k=v …]` |
| Download from SRA (local scratch) | `abc data download --tool s5cmd --tool-args '--no-sign-request' --source <s3-url> --driver exec` |
| Download from SRA → research-group bucket | `abc data download --tool s5cmd --tool-args '--no-sign-request' --source <s3-url> --destination storage --driver exec` |
| Run demo pipeline | `abc pipeline run nextflow-io/hello` |
| Run a module | `abc module run nf-core/<module> --samplesheet <csv>` |

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `connect: connection refused` | You need to be on the Stellenbosch network or Tailscale VPN to reach the cluster |
| `403 Forbidden` | `abc context show` — confirm the **aither** context is active and your token is set |
| Job goes to wrong namespace | `abc context show` — the `nomad_namespace` field in your config controls the default |
| `unknown command` | `abc --help` for the command list; `abc <verb> --help` for flags |

---

## Next steps

- **[Reference → job run](./docs/reference/jobs.md)** — full `#ABC` directive table and all flags.
- **[Reference → data](./docs/reference/data.md)** — upload, download, and object storage.
- **[`USAGE.md`](USAGE.md)** — every command, flag, and environment variable.
