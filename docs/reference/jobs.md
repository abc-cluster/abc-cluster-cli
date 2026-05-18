---
sidebar_position: 6
---

# job run

Convert annotated shell scripts into Nomad batch jobs and submit them to the ABC cluster.

## Basic usage

```bash
abc job run <script.sh> [flags]
```

`abc job run` parses `#ABC` directives from the script preamble, generates a Nomad job HCL, and submits it.  
Pass `--dry-run` to print the generated HCL without submitting.

## Key flags

| Flag | Description |
|---|---|
| `--name NAME` | Override the Nomad job name |
| `--runtime RUNTIME` | Runtime identifier (`pixi-exec`, `micromamba-exec`, …) |
| `--from PATH` | Path to the runtime definition file (`pixi.toml`, `pixi.lock`, `environment.yml`) |
| `--driver DRIVER` | Nomad task driver (`exec`, `docker`) |
| `--cores N` | CPU core count |
| `--mem SIZE` | Memory allocation (`512M`, `8G`, `4096`) |
| `--time HH:MM:SS` | Walltime limit |
| `--task-tmp` | Mount a per-task scratch directory at `$TMPDIR` |
| `--pixi-cleanup` | Remove the pixi environment from disk after the job finishes |
| `--mamba-cleanup` | Remove the micromamba environment from disk after the job finishes |
| `--meta key=value` | Pass Nomad task metadata (repeatable) |
| `--dry-run` | Print Nomad HCL without submitting |
| `--detach` | Return immediately after submit; do not tail logs |
| `--namespace NS` | Nomad namespace (default: from config) |
| `--no-network` | Disable network access inside the task |

## `#ABC` preamble directives

All flags available on the command line can also be embedded directly in the script using `#ABC` comment lines. The CLI merges preamble directives with any CLI flags — CLI flags win on conflict.

```bash
#!/bin/bash
#ABC --name=my-analysis
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
```

### Full directive reference

#### Resources

| Directive | Example | Description |
|---|---|---|
| `--name=NAME` | `--name=trim-reads` | Nomad job name |
| `--cores=N` | `--cores=8` | CPU cores |
| `--mem=SIZE` | `--mem=16G` | Memory (`512`, `2G`, `4096M`) |
| `--time=HH:MM:SS` | `--time=04:00:00` | Walltime limit |
| `--gpus=N` | `--gpus=1` | GPU count |
| `--nodes=N` | `--nodes=2` | Number of Nomad allocations |
| `--priority=N` | `--priority=50` | Nomad job priority (1–100) |

#### Targeting

| Directive | Example | Description |
|---|---|---|
| `--driver=DRIVER` | `--driver=exec` | Nomad task driver (`exec`, `docker`) |
| `--dc=NAME` | `--dc=dc1` | Target datacenter (repeatable) |
| `--namespace=NS` | `--namespace=research` | Nomad namespace |
| `--region=NAME` | `--region=global` | Nomad region |
| `--constraint=EXPR` | `--constraint=attr.cpu.arch=amd64` | Hard placement constraint |
| `--affinity=EXPR` | `--affinity=attr.datacenter=dc1` | Soft placement preference |
| `--spread` | | Spread allocations across nodes |

#### Runtimes

| Directive | Example | Description |
|---|---|---|
| `--runtime=RUNTIME` | `--runtime=pixi-exec` | Runtime to use (see [Runtimes](#runtimes) below) |
| `--from=PATH` | `--from=pixi.toml` | Runtime definition file |
| `--pixi-cleanup` | | Remove pixi env from disk after the job completes |
| `--mamba-cleanup` | | Remove micromamba env from disk after the job completes |
| `--conda=ENV` | `--conda=bioinf` | Named conda environment (legacy) |
| `--task-tmp` | | Mount a scratch dir at `$TMPDIR` |
| `--no-network` | | Disable outbound network |

#### Wave tool injection

| Directive | Example | Description |
|---|---|---|
| `--wave-inject-tools` | *(bare flag)* | Inject all `wave_inject` tools into the base image via Wave Lite |
| `--wave-inject-tools=NAMES` | `--wave-inject-tools=s5cmd` | Inject specific tools only (comma-separated) |

#### Job metadata & secrets

| Directive | Example | Description |
|---|---|---|
| `--meta=key=value` | `--meta=sample=SRR001` | Nomad task metadata (repeatable) |
| `--port=LABEL` | `--port=http` | Expose a dynamic port |

#### Logging & I/O

| Directive | Example | Description |
|---|---|---|
| `--output=PATH` | `--output=/logs/job.out` | Redirect stdout |
| `--error=PATH` | `--error=/logs/job.err` | Redirect stderr |
| `--chdir=PATH` | `--chdir=/data` | Set working directory |

#### Rescheduling

| Directive | Example | Description |
|---|---|---|
| `--reschedule-mode=MODE` | `--reschedule-mode=delay` | Reschedule mode (`delay`, `unlimited`) |
| `--reschedule-attempts=N` | `--reschedule-attempts=3` | Max reschedule attempts |
| `--reschedule-interval=DUR` | `--reschedule-interval=1h` | Reschedule evaluation interval |
| `--reschedule-delay=DUR` | `--reschedule-delay=30s` | Initial reschedule delay |
| `--reschedule-max-delay=DUR` | `--reschedule-max-delay=10m` | Maximum reschedule delay |

#### Nomad runtime environment

The following flags inject Nomad runtime variables as environment variables inside the task. Add any combination to your preamble.

| Directive | Env var injected |
|---|---|
| `--alloc_id` | `NOMAD_ALLOC_ID` |
| `--short_alloc_id` | `NOMAD_SHORT_ALLOC_ID` |
| `--alloc_name` | `NOMAD_ALLOC_NAME` |
| `--alloc_index` | `NOMAD_ALLOC_INDEX` |
| `--job_id` | `NOMAD_JOB_ID` |
| `--job_name` | `NOMAD_JOB_NAME` |
| `--parent_job_id` | `NOMAD_PARENT_JOB_ID` |
| `--group_name` | `NOMAD_GROUP_NAME` |
| `--task_name` | `NOMAD_TASK_NAME` |
| `--cpu_limit` | `NOMAD_CPU_LIMIT` |
| `--cpu_cores` | `NOMAD_CPU_CORES` |
| `--mem_limit` | `NOMAD_MEMORY_LIMIT` |
| `--mem_max_limit` | `NOMAD_MEMORY_MAX_LIMIT` |
| `--alloc_dir` | `NOMAD_ALLOC_DIR` |
| `--task_dir` | `NOMAD_TASK_DIR` |
| `--secrets_dir` | `NOMAD_SECRETS_DIR` |
| `--namespace` | `NOMAD_NAMESPACE` *(bare flag, no value)* |
| `--dc` | `NOMAD_DC` *(bare flag, no value)* |
| `--hpc_compat_env` | Full HPC-compatibility env block (SLURM_* aliases) |

---

## Runtimes

The `--runtime` directive selects the software packaging system used to set up the environment before your script runs. The CLI generates a self-contained wrapper that downloads the runtime binary from the cluster binary store, installs the environment, and re-execs your script inside it — no pre-installed tooling on compute nodes is required.

### `pixi-exec` — Pixi environments

Installs a [Pixi](https://pixi.sh) conda/PyPI environment from a `pixi.toml` manifest. Pixi is downloaded at job start; the environment is created inside the task directory.

**Aliases:** `pixi-exec`, `pixi`

#### From a local `pixi.toml`

```bash
#!/bin/bash
#ABC --name=trim-reads
#ABC --runtime=pixi-exec
#ABC --from=pixi.toml          # path resolved at submit time
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id

set -euo pipefail
fastp --in1 "${NOMAD_META_R1}" --in2 "${NOMAD_META_R2}" ...
```

Submit:

```bash
abc job run 01-trim-reads.sh \
  --meta sample=SRR123 \
  --meta r1=/data/raw/SRR123_R1.fastq.gz \
  --meta r2=/data/raw/SRR123_R2.fastq.gz \
  --meta outdir=/data/trimmed
```

At runtime, the CLI:
1. Embeds `pixi.toml` as a Nomad template (`local/pixi.toml`)
2. Downloads the Pixi binary from the cluster binary store via `curl`
3. Runs `pixi install --manifest-path local/pixi.toml`
4. Re-execs your script inside the environment via `pixi run`
5. On exit, removes the environment (if `--pixi-cleanup`)

#### From a locked `pixi.lock` (reproducible installs)

For bit-for-bit reproducibility across runs, point `--from` at a `pixi.lock` file. The CLI automatically locates the companion `pixi.toml` in the same directory, embeds both files, and passes `--locked` to `pixi install`.

```bash
#!/bin/bash
#ABC --name=trim-reads-locked
#ABC --runtime=pixi-exec
#ABC --from=pixi.lock          # companion pixi.toml must exist alongside it
#ABC --cores=8
#ABC --mem=16G
#ABC --time=04:00:00
#ABC --task-tmp
#ABC --pixi-cleanup
#ABC --alloc_id
```

Generate and commit your lock file locally before submitting:

```bash
# Generate the lock file on your local machine
pixi install            # creates pixi.lock

# Commit both files
git add pixi.toml pixi.lock
git commit -m "lock environment for reproducibility"

# Submit — both files are embedded in the Nomad job
abc job run 01-trim-reads.sh
```

:::tip
Use `pixi.lock` mode whenever you need guaranteed reproducibility — e.g., publication-ready analyses or long-running production workflows.
:::

### `micromamba-exec` — Conda environments

Installs a conda environment from an `environment.yml` file using [micromamba](https://mamba.readthedocs.io/en/latest/user_guide/micromamba.html). The micromamba binary is downloaded from the cluster binary store at job start; the environment is installed under the task directory.

**Aliases:** `micromamba-exec`, `micromamba`, `mamba`

```bash
#!/bin/bash
#ABC --name=align-bwa
#ABC --runtime=micromamba-exec
#ABC --from=environment.yml    # path resolved at submit time
#ABC --cores=16
#ABC --mem=32G
#ABC --time=08:00:00
#ABC --task-tmp
#ABC --mamba-cleanup
#ABC --alloc_id

set -euo pipefail
bwa-mem2 mem -t "${NOMAD_CPU_CORES}" "${NOMAD_META_REF}" \
  "${NOMAD_META_R1}" "${NOMAD_META_R2}" | samtools sort ...
```

A minimal `environment.yml`:

```yaml
name: bio-env
channels:
  - conda-forge
  - bioconda
dependencies:
  - bwa-mem2=2.2.1
  - samtools=1.21
  - fastp=0.23.4
  - fastqc=0.12.1
```

At runtime, the CLI:
1. Embeds `environment.yml` as a Nomad template (`local/environment.yml`)
2. Downloads the micromamba binary from the cluster binary store via `curl`
3. Runs `micromamba env create --file local/environment.yml --prefix ${NOMAD_TASK_DIR}/.mamba/env`
4. Re-execs your script via `micromamba run --prefix ...`
5. On exit, removes the environment directory (if `--mamba-cleanup`)

#### Environment cleanup

Both runtimes support automatic cleanup so conda/pixi environments do not accumulate on compute nodes after a job finishes:

```bash
#ABC --pixi-cleanup      # removes ${NOMAD_TASK_DIR}/.pixi on EXIT
#ABC --mamba-cleanup     # removes ${NOMAD_TASK_DIR}/.mamba on EXIT
```

These can also be passed as CLI flags:

```bash
abc job run script.sh --pixi-cleanup
abc job run script.sh --mamba-cleanup
```

---

## Wave tool injection

`--wave-inject-tools` augments any Docker base image with cluster-managed binaries at submit time, without rebuilding the image. The CLI calls the cluster's Wave Lite service (`abc-wave`) at submit time, which produces a new image tag that is wired directly into the job's `image` field. No Dockerfile change, no prestart task, no volume mount.

### How it works

1. `abc admin tools push` builds two kinds of wave layer archives per tool and uploads them to cluster S3:
   - **Combined layer** `wave-layer-linux-<arch>.tar.gz` — all tools marked `wave_inject = true` in `tools.toml`
   - **Per-tool layers** `wave-layer-linux-<arch>-<tool>.tar.gz` — one archive per tool
2. At submit time, `abc job run` resolves which layer(s) to use, downloads them to compute their sha256 digests, then calls `POST /v1alpha2/container` on the Wave Lite service with the S3 layer URL(s). Wave fetches each layer server-side (no local size limit).
3. The resolved `targetImage` (e.g. `wave.seedling.abc-cluster.cloud/wt/<token>/library/ubuntu:24.04`) replaces the original `image` in the generated HCL.

### Layer selection

| Directive | Layer used | Use when |
|---|---|---|
| `--wave-inject-tools` *(bare)* | Combined layer — all `wave_inject` tools | You want everything available |
| `--wave-inject-tools=s5cmd` | Per-tool layer for `s5cmd` only | Minimal image, one tool |
| `--wave-inject-tools=s5cmd,rclone` | Two per-tool layers merged by Wave | Specific subset of tools |

### Example — inject all tools

```bash
#!/bin/bash
#ABC --name=sync-job
#ABC --driver=docker
#ABC --driver.config.image=ubuntu:24.04
#ABC --wave-inject-tools
#ABC --mem=1G
#ABC --cores=2

# s5cmd and rclone are available inside the container
s5cmd --endpoint-url "$S3_ENDPOINT" cp s3://my-bucket/input.bam .
rclone copy remote:backup/ /data/restore/
```

### Example — inject a single tool

```bash
#!/bin/bash
#ABC --name=s3-upload
#ABC --driver=docker
#ABC --driver.config.image=alpine:3.21
#ABC --wave-inject-tools=s5cmd
#ABC --mem=256M
#ABC --cores=1

s5cmd --endpoint-url "$S3_ENDPOINT" cp results/ s3://my-bucket/results/
```

### Example — inject a named subset (multiple tools, per-tool layers)

```bash
#!/bin/bash
#ABC --name=archive-job
#ABC --driver=docker
#ABC --driver.config.image=debian:12-slim
#ABC --wave-inject-tools=s5cmd,rclone
#ABC --mem=512M
#ABC --cores=1

# Wave merges two per-tool layers into a single augmented image
s5cmd ...
rclone ...
```

### Prerequisites

- `abc admin tools push` must have been run at least once to upload the wave layers.
- The active context must have `admin.services.wave.http` set to the Wave Lite URL:
  ```bash
  abc config set contexts.<name>.admin.services.wave.http http://<wave-host>:<port>
  ```
- `--wave-inject-tools` requires `--driver=docker`. It cannot be combined with `--runtime=wave-exec`.

### Marking a tool for wave injection

In `~/.abc/assets/tools.toml`, add `wave_inject = true` to any tool you want available for injection:

```toml
[tools.s5cmd]
repo        = "peak/s5cmd"
version     = "v2.3.0"
wave_inject = true

[tools.rclone]
repo        = "rclone/rclone"
version     = "v1.73.5"
wave_inject = true
```

Then rebuild and push the wave layers:

```bash
abc admin tools push
```

See [`abc admin tools`](./admin-tools.md) for the full tools management reference.

---

## Passing data to jobs via metadata

Use `--meta key=value` (or `#ABC --meta key=val` in the preamble) to pass per-run parameters. Inside the task, metadata appears as `NOMAD_META_<KEY>` environment variables (keys are uppercased automatically).

```bash
abc job run align.sh \
  --meta sample=SRR123 \
  --meta r1=/data/raw/SRR123_R1.fastq.gz \
  --meta r2=/data/raw/SRR123_R2.fastq.gz \
  --meta ref=/data/ref/GRCh38.fa \
  --meta outdir=/data/bam
```

Inside the script:

```bash
SAMPLE="${NOMAD_META_SAMPLE:?}"
R1="${NOMAD_META_R1:?}"
REF="${NOMAD_META_REF:?}"
```

:::note
Using `:?` in parameter expansion causes the script to exit immediately with a helpful error if a required metadata key is missing.
:::

---

## Job lifecycle commands

```bash
abc job list                   # list recent jobs in the current namespace
abc job show <id>              # detailed status and task breakdown
abc job stop <id>              # cancel a running job
abc job logs <id>              # tail allocation logs (stdout + stderr)
abc job status <id>            # short status summary
abc job dispatch <id>          # dispatch a parameterised job
abc job translate <script.sh>  # print generated Nomad HCL without submitting
abc job trace <id>             # trace Nomad allocation events
```

---

## Examples

### Bioinformatics pipeline steps (pixi)

```bash
# Trim reads
abc job run examples/pixi-bio/01-trim-reads.sh \
  --meta sample=SRR123 \
  --meta r1=/data/raw/SRR123_R1.fastq.gz \
  --meta r2=/data/raw/SRR123_R2.fastq.gz \
  --meta outdir=/data/trimmed

# Align to reference
abc job run examples/pixi-bio/02-align-bwa.sh \
  --meta sample=SRR123 \
  --meta r1=/data/trimmed/SRR123_R1.trimmed.fastq.gz \
  --meta r2=/data/trimmed/SRR123_R2.trimmed.fastq.gz \
  --meta ref=/data/ref/GRCh38.fa \
  --meta outdir=/data/bam

# Call variants
abc job run examples/pixi-bio/03-bcftools-call.sh \
  --meta sample=SRR123 \
  --meta bam=/data/bam/SRR123.bam \
  --meta ref=/data/ref/GRCh38.fa \
  --meta outdir=/data/vcf
```

### Bioinformatics pipeline steps (micromamba)

```bash
# Trim with fastp + FastQC
abc job run examples/mamba-bio/01-trim-reads.sh \
  --meta sample=SRR123 \
  --meta r1=/data/raw/SRR123_R1.fastq.gz \
  --meta r2=/data/raw/SRR123_R2.fastq.gz \
  --meta outdir=/data/trimmed

# Align with BWA-MEM2
abc job run examples/mamba-bio/02-align-bwa.sh \
  --meta sample=SRR123 \
  --meta r1=/data/trimmed/SRR123_R1.trimmed.fastq.gz \
  --meta r2=/data/trimmed/SRR123_R2.trimmed.fastq.gz \
  --meta ref=/data/ref/GRCh38.fa \
  --meta outdir=/data/bam

# MultiQC report
abc job run examples/mamba-bio/04-multiqc-report.sh \
  --meta indir=/data/trimmed \
  --meta outdir=/data/reports \
  --meta title="Run 2026-Q1"
```

### Inspect generated HCL before submitting

```bash
abc job run script.sh --dry-run
```

### Override resource directives at submit time

Flags on the command line always override `#ABC` preamble directives:

```bash
abc job run script.sh --cores 32 --mem 64G --time 12:00:00
```

---

## See also

- [`abc pipeline run`](./pipeline.md) — run a Nextflow pipeline as a head job with Wave container builds
- [`abc module run`](./module.md) — run a single nf-core module via `nf-pipeline-gen`
- [`abc data`](./data.md) — stage input/output data to/from MinIO or JuiceFS
- [`abc admin tools`](./admin-tools.md) — manage cluster tool binaries, wave layers, and binary store
