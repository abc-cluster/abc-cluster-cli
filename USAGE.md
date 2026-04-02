# abc CLI — Command Reference

This document describes every command available in the `abc` CLI.

## Table of Contents

- [Global flags](#global-flags)
- [pipeline run](#pipeline-run)
- [job run](#job-run)
  - [Preamble directives](#preamble-directives)
  - [Directive precedence](#directive-precedence)
- [job list](#job-list)
- [job show](#job-show)
- [job stop](#job-stop)
- [job logs](#job-logs)
- [job status](#job-status)
- [job dispatch](#job-dispatch)
- [data upload](#data-upload)
- [data encrypt](#data-encrypt)
- [data decrypt](#data-decrypt)
- [data download](#data-download)

---

## Global flags

These flags are available on every `abc` command.

| Flag              | Env var             | Description                                      | Default                      |
|-------------------|---------------------|--------------------------------------------------|------------------------------|
| `--url`           | `ABC_API_ENDPOINT`  | abc-cluster API endpoint URL                     | `https://api.abc-cluster.io` |
| `--access-token`  | `ABC_ACCESS_TOKEN`  | abc-cluster access token                         | *(unset)*                    |
| `--workspace`     | `ABC_WORKSPACE_ID`  | Workspace ID                                     | *(user's default workspace)* |

---

## `pipeline run`

Submit a pipeline for execution on the abc-cluster platform.

```
abc pipeline run --pipeline <name-or-url> [flags]
```

### Flags

| Flag            | Short | Description                                              |
|-----------------|-------|----------------------------------------------------------|
| `--pipeline`    | `-p`  | Pipeline name or URL to run (**required**)               |
| `--name`        |       | Custom name for this run                                 |
| `--revision`    |       | Pipeline revision (branch, tag, or commit SHA)           |
| `--profile`     |       | Nextflow config profile(s) to use (comma-separated)      |
| `--work-dir`    |       | Work directory for pipeline execution                    |
| `--params-file` |       | Path to a YAML or JSON file with pipeline parameters     |
| `--config`      |       | Path to a Nextflow config file to use for this run       |

### Examples

```bash
# Run a pipeline by GitHub URL
abc pipeline run --pipeline https://github.com/org/my-pipeline

# Run with a specific revision and profile
abc pipeline run --pipeline my-pipeline --revision main --profile test

# Run with parameters from a YAML file
abc pipeline run --pipeline my-pipeline --params-file params.yaml

# Run with a custom work directory and run name
abc pipeline run \
  --pipeline my-pipeline \
  --name my-analysis-run \
  --work-dir s3://my-bucket/work \
  --revision v2.1.0 \
  --profile production
```

---

## `job run`

Parse `#ABC`/`#NOMAD` preamble directives from an annotated shell script and produce a Nomad HCL batch job spec. Without `--submit` the generated HCL is printed to stdout; with `--submit` it is registered directly with the Nomad server.

```
abc job run <script> [flags]
```

### Submission flags

| Flag              | Description                                                  |
|-------------------|--------------------------------------------------------------|
| `--submit`        | Submit the job to Nomad instead of printing HCL              |
| `--dry-run`       | Plan the job server-side without submitting                  |
| `--watch`         | Stream logs immediately after `--submit`                     |
| `--output-file`   | Write generated HCL to a file instead of stdout             |

### Scheduler flags (Class 1)

These flags configure Nomad HCL stanza fields and can also be set via script preamble directives.

| Flag                            | Preamble directive                  | Description                                                       |
|---------------------------------|-------------------------------------|-------------------------------------------------------------------|
| `--name`                        | `--name=<string>`                   | Job name (default: script filename stem)                          |
| `--namespace`                   | `--namespace=<string>`              | Nomad namespace                                                   |
| `--region`                      | `--region=<string>`                 | Nomad region                                                      |
| `--dc`                          | `--dc=<datacenter>`                 | Target datacenter (repeatable)                                    |
| `--priority`                    | `--priority=<1-100>`                | Scheduler priority (default: 50)                                  |
| `--nodes`                       | `--nodes=<int>`                     | Parallel group instances / array width (default: 1)               |
| `--cores`                       | `--cores=<int>`                     | CPU cores per task                                                |
| `--mem`                         | `--mem=<size>[K\|M\|G]`             | Memory per task (e.g. `4G`, `512M`)                               |
| `--gpus`                        | `--gpus=<int>`                      | GPU count (nvidia/gpu device plugin)                              |
| `--time`                        | `--time=<HH:MM:SS>`                 | Walltime limit; wraps command in `timeout(1)`                     |
| `--chdir`                       | `--chdir=<path>`                    | Working directory inside the task sandbox                         |
| `--driver`                      | `--driver=<string>`                 | Task driver: `exec` (default), `hpc-bridge`, `docker`             |
| `--depend`                      | `--depend=<complete:job-id>`        | Block on another job via prestart lifecycle hook                  |
| `--output`                      | `--output=<filename>`               | Tee stdout to `$NOMAD_TASK_DIR/<filename>`                        |
| `--error`                       | `--error=<filename>`                | Tee stderr to `$NOMAD_TASK_DIR/<filename>`                        |
| `--no-network`                  | `--no-network`                      | Disable network access (Nomad mode = `"none"`)                    |
| `--port`                        | `--port=<label>`                    | Named dynamic port; injects `NOMAD_IP/PORT/ADDR_<label>`          |
| `--constraint=<attr><op><val>`  | `--constraint=<attr><op><val>`      | Nomad placement constraint (repeatable). Ops: `== != =~ !~ < <= > >=` |
| `--affinity=<expr>[,weight=N]`  | `--affinity=<expr>[,weight=N]`      | Nomad placement affinity (repeatable)                             |
| *(preamble only)*               | `--driver.config.<key>=<val>`       | Arbitrary driver config field                                     |

#### Reschedule flags

| Flag                        | Preamble directive                  | Description                               |
|-----------------------------|-------------------------------------|-------------------------------------------|
| `--reschedule-mode`         | `--reschedule-mode=<delay\|fail>`   | Reschedule policy mode                    |
| `--reschedule-attempts`     | `--reschedule-attempts=<int>`       | Maximum reschedule attempts               |
| `--reschedule-interval`     | `--reschedule-interval=<duration>`  | Reschedule evaluation window (e.g. `30s`) |
| `--reschedule-delay`        | `--reschedule-delay=<duration>`     | Base reschedule delay (e.g. `5s`)         |
| `--reschedule-max-delay`    | `--reschedule-max-delay=<duration>` | Maximum reschedule delay (e.g. `1m`)      |

### Runtime-exposure flags (Class 2)

These preamble directives inject the corresponding `NOMAD_*` variable into the task's environment block so the script can read the value at execution time. `NOMAD_REGION` is always injected automatically by Nomad. PBS and SLURM compatibility aliases are always emitted.

#### Task identity

| Preamble directive  | Env var injected      | Notes                                    |
|---------------------|-----------------------|------------------------------------------|
| `--alloc_id`        | `NOMAD_ALLOC_ID`      | Unique per execution                     |
| `--short_alloc_id`  | `NOMAD_SHORT_ALLOC_ID`|                                          |
| `--alloc_name`      | `NOMAD_ALLOC_NAME`    | `<job>.<group>[<index>]`                 |
| `--alloc_index`     | `NOMAD_ALLOC_INDEX`   | 0-based; use to shard array jobs         |
| `--job_id`          | `NOMAD_JOB_ID`        |                                          |
| `--job_name`        | `NOMAD_JOB_NAME`      |                                          |
| `--parent_job_id`   | `NOMAD_JOB_PARENT_ID` | Dispatched jobs only                     |
| `--group_name`      | `NOMAD_GROUP_NAME`    |                                          |
| `--task_name`       | `NOMAD_TASK_NAME`     |                                          |
| `--namespace`       | `NOMAD_NAMESPACE`     | Use without `=<value>` to expose env only|
| `--dc`              | `NOMAD_DC`            | Use without `=<value>` to expose env only|

#### Resources

| Preamble directive | Env var injected         | Notes                              |
|--------------------|--------------------------|------------------------------------|
| `--cpu_limit`      | `NOMAD_CPU_LIMIT`        | MHz                                |
| `--cpu_cores`      | `NOMAD_CPU_CORES`        | Use for `-t` in BWA/samtools/STAR  |
| `--mem_limit`      | `NOMAD_MEMORY_LIMIT`     | MB; use for JVM `-Xmx`             |
| `--mem_max_limit`  | `NOMAD_MEMORY_MAX_LIMIT` |                                    |

#### Directories

| Preamble directive | Env var injected      | Notes                              |
|--------------------|-----------------------|------------------------------------|
| `--alloc_dir`      | `NOMAD_ALLOC_DIR`     | Shared across the task group       |
| `--task_dir`       | `NOMAD_TASK_DIR`      | Per-task private scratch space     |
| `--secrets_dir`    | `NOMAD_SECRETS_DIR`   | In-memory, noexec                  |

### Meta flags (Class 3)

| Flag / Directive            | Description                                                          |
|-----------------------------|----------------------------------------------------------------------|
| `--meta=<key>=<value>`      | Nomad meta block entry (repeatable). Key is uppercased for env access as `NOMAD_META_<KEY>`. |

### Params file

| Flag               | Description                                                        |
|--------------------|--------------------------------------------------------------------|
| `--params-file`    | YAML file with directive key/value pairs (lowest priority after env vars). Nested keys are dot-flattened: `cores: 8` → `--cores=8`. |

### Nomad connection flags

These flags apply to all `abc job` subcommands that communicate with Nomad.

| Flag            | Env var                     | Description                                     | Default                   |
|-----------------|-----------------------------|-------------------------------------------------|---------------------------|
| `--nomad-addr`  | `ABC_ADDR` / `NOMAD_ADDR`   | Nomad API address                               | `http://127.0.0.1:4646`   |
| `--nomad-token` | `ABC_TOKEN` / `NOMAD_TOKEN` | Nomad ACL token                                 | *(unset)*                 |
| `--region`      | `ABC_REGION` / `NOMAD_REGION` | Nomad region                                  | *(unset)*                 |

### Directive precedence

From highest to lowest priority:

```
CLI flags  >  #ABC preamble  >  #NOMAD preamble  >  NOMAD_* env vars  >  params file
```

### Preamble directives

Scripts can include a block of `#ABC` or `#NOMAD` comment directives before the first non-comment line. Both prefix styles accept the same directive keys. Inline shell comments after a space-hash are stripped, so annotated preambles are valid:

```bash
#ABC --cores=8    # 8 cores per task (same as SLURM --cpus-per-task)
```

### Examples

```bash
# Print generated HCL (no cluster needed)
abc job run bwa-align.sh

# Pipe to Nomad directly
abc job run bwa-align.sh | nomad job run -

# Dry-run: plan server-side, show placement feasibility
abc job run bwa-align.sh --dry-run --region za-cpt

# Submit and tail logs immediately
abc job run bwa-align.sh --submit --region za-cpt --watch

# Override a preamble directive from the CLI
abc job run bwa-align.sh --submit --nodes=96 --cores=16

# Write generated HCL to a file
abc job run bwa-align.sh --output-file bwa-align.hcl

# Use a YAML params file
abc job run bwa-align.sh --params-file job-params.yaml --submit
```

### Annotated script example

```bash
#!/bin/bash
#ABC --name=ocean-model
#ABC --nodes=4
#ABC --cores=28
#ABC --mem=64G
#ABC --time=02:00:00
#ABC --alloc_id          # expose NOMAD_ALLOC_ID
#ABC --alloc_index       # expose NOMAD_ALLOC_INDEX (0-based, for sharding)
#ABC --task_dir          # expose NOMAD_TASK_DIR
#ABC --cpu_cores         # expose NOMAD_CPU_CORES (use for -t in tools)
#ABC --meta=sample_id=S001

mpirun -np 112 ./ocean_model
```

---

## `job list`

List Nomad batch jobs.

```
abc job list [flags]
```

### Flags

| Flag          | Description                                          | Default |
|---------------|------------------------------------------------------|---------|
| `--status`    | Filter by status: `running`, `complete`, `dead`, `pending` | *(all)* |
| `--region`    | Filter by Nomad region                               | *(all)* |
| `--namespace` | Filter by namespace                                  | *(all)* |
| `--limit`     | Maximum number of results to show                    | `20`    |

### Example

```bash
# List running jobs
abc job list --status running

# List all jobs in a specific namespace
abc job list --namespace my-ns --limit 50
```

---

## `job show`

Show detailed information about a Nomad batch job, including task groups and recent allocations.

```
abc job show <job-id> [flags]
```

### Flags

| Flag          | Description      |
|---------------|------------------|
| `--namespace` | Nomad namespace  |

### Example

```bash
abc job show script-job-bwa-align-a1b2c3d4
```

---

## `job stop`

Stop a running Nomad batch job.

```
abc job stop <job-id> [flags]
```

### Flags

| Flag          | Description                                            |
|---------------|--------------------------------------------------------|
| `--purge`     | Remove the job definition from Nomad after stopping    |
| `--detach`    | Return immediately without waiting for the stop        |
| `--yes`       | Skip the confirmation prompt                           |
| `--namespace` | Nomad namespace                                        |

### Example

```bash
# Stop with confirmation prompt
abc job stop script-job-bwa-align-a1b2c3d4

# Stop and purge without prompting
abc job stop script-job-bwa-align-a1b2c3d4 --purge --yes
```

---

## `job logs`

Stream or print logs for a Nomad batch job.

```
abc job logs <job-id> [flags]
```

### Flags

| Flag          | Short | Description                                              |
|---------------|-------|----------------------------------------------------------|
| `--follow`    | `-f`  | Stream logs in real time                                 |
| `--alloc`     |       | Filter to a specific allocation ID prefix                |
| `--task`      |       | Task name within the allocation (default: `main`)        |
| `--type`      |       | Log stream: `stdout` or `stderr` (default: `stdout`)     |
| `--namespace` |       | Nomad namespace                                          |
| `--since`     |       | Show logs since this timestamp (RFC3339)                 |
| `--output`    |       | Write stdout logs to a file (requires `--type stdout`)   |
| `--error`     |       | Write stderr logs to a file (requires `--type stderr`)   |

### Examples

```bash
# Tail live logs
abc job logs script-job-bwa-align-a1b2c3d4 --follow

# View stderr for a specific allocation
abc job logs my-job --type stderr --alloc a1b2c3d4

# Save stdout to a file
abc job logs my-job --output job-output.txt
```

---

## `job status`

Print a compact one-line status summary for a Nomad batch job and exit with a code reflecting the job outcome.

```
abc job status <job-id> [flags]
```

### Exit codes

| Code | Meaning                                 |
|------|-----------------------------------------|
| `0`  | Job complete with no failures           |
| `1`  | Job dead or failed                      |
| `2`  | Job still running or pending            |
| `3`  | Error reaching Nomad or job not found   |

### Flags

| Flag          | Description      |
|---------------|------------------|
| `--namespace` | Nomad namespace  |

### Example

```bash
abc job status script-job-bwa-align-a1b2c3d4
echo "Exit code: $?"
```

---

## `job dispatch`

Dispatch an instance of a parameterized Nomad batch job.

```
abc job dispatch <job-id> [flags]
```

### Flags

| Flag       | Description                                                          |
|------------|----------------------------------------------------------------------|
| `--meta`   | Meta key=value pair to pass to the dispatched job (repeatable)       |
| `--input`  | Path to a file whose contents are passed as the dispatch payload     |
| `--detach` | Do not wait for the dispatched allocation to start                   |

### Example

```bash
abc job dispatch my-parameterized-job \
  --meta sample_id=S001 \
  --meta lane=L001 \
  --input payload.bin
```

---

## `data upload`

Upload a local file or folder to the abc-cluster data service using the tus resumable upload protocol.

```
abc data upload <path> [flags]
```

### Flags

| Flag               | Env var                | Description                                                                              |
|--------------------|------------------------|------------------------------------------------------------------------------------------|
| `--name`           |                        | Display name for the uploaded file                                                        |
| `--endpoint`       | `ABC_UPLOAD_ENDPOINT`  | Tus upload endpoint URL (defaults to `<url>/data/uploads`)                                |
| `--upload-token`   | `ABC_UPLOAD_TOKEN`     | Bearer token for tus uploads (falls back to `--access-token`)                             |
| `--crypt-password` |                        | rclone crypt password for client-side encryption before upload                            |
| `--crypt-salt`     |                        | rclone crypt salt (password2) for client-side encryption                                  |
| `--checksum`       |                        | Include SHA-256 checksum metadata in the tus upload (default: `true`)                     |
| `--progress`       |                        | Show live progress bars (default: `true`)                                                 |
| `--parallel`       |                        | Upload directory files in parallel (default: `true`)                                      |
| `--parallel-jobs`  |                        | Number of parallel upload workers (default: number of CPU cores)                          |
| `--chunk-size`     |                        | Upload chunk size (e.g. `64MB`, `2MiB`; default: `64MB`)                                 |
| `--max-rate`       |                        | Maximum upload throughput (e.g. `50MB/s`); default is unlimited                           |
| `--meta`           |                        | Additional tus upload metadata as `key=value` (repeatable)                                |
| `--no-resume`      |                        | Ignore stored resume state and always start a fresh upload                                |
| `--status`         |                        | Show stored tus resume state for the file (does not upload)                               |
| `--clear`          |                        | Clear stored tus resume state for the file (does not upload)                              |

### Examples

```bash
# Upload a file
abc data upload ./data.csv

# Upload with a display name
abc data upload ./data.csv --name sample-data

# Upload all files from a folder (recursively, in parallel)
abc data upload ./dataset

# Upload with client-side encryption
abc data upload ./data.csv --crypt-password "secret" --crypt-salt "pepper"

# Upload using a dedicated tus bearer token
ABC_UPLOAD_TOKEN=<tusd-bearer-token> abc data upload ./data.csv

# Upload using a dedicated endpoint
ABC_UPLOAD_ENDPOINT=https://dev.abc-cluster.cloud/files abc data upload ./data.csv

# Check resume state without uploading
abc data upload ./data.csv --status

# Clear resume state and re-upload from scratch
abc data upload ./data.csv --clear
abc data upload ./data.csv --no-resume
```

---

## `data encrypt`

Encrypt a local file or folder using the rclone crypt format so it can be uploaded and later decrypted with rclone or `abc data decrypt`.

```
abc data encrypt <path> [flags]
```

### Flags

| Flag               | Description                                                        |
|--------------------|--------------------------------------------------------------------|
| `--crypt-password` | rclone crypt password (**required**)                               |
| `--crypt-salt`     | rclone crypt salt (password2)                                      |
| `--output`         | Output file path for single-file encryption                        |
| `--output-dir`     | Output directory for folder encryption                             |
| `--progress`       | Show live progress bars (default: `true`)                          |

### Examples

```bash
# Encrypt a single file (output: data.csv.bin)
abc data encrypt ./data.csv --crypt-password "secret"

# Encrypt a file to a specific path
abc data encrypt ./data.csv --crypt-password "secret" --output ./data.csv.enc

# Encrypt a folder (output directory: ./dataset-encrypted)
abc data encrypt ./dataset --crypt-password "secret" --crypt-salt "pepper"

# Encrypt and then upload the encrypted file
abc data encrypt ./data.csv --crypt-password "secret"
abc data upload ./data.csv.bin
```

---

## `data decrypt`

Decrypt a local file or folder previously encrypted with `abc data encrypt` or rclone crypt.

```
abc data decrypt <path> [flags]
```

### Flags

| Flag               | Description                                                        |
|--------------------|--------------------------------------------------------------------|
| `--crypt-password` | rclone crypt password (**required**)                               |
| `--crypt-salt`     | rclone crypt salt (password2)                                      |
| `--output`         | Output file path for single-file decryption                        |
| `--output-dir`     | Output directory for folder decryption                             |

### Examples

```bash
# Decrypt a single file
abc data decrypt ./data.csv.bin --crypt-password "secret"

# Decrypt to a specific path
abc data decrypt ./data.csv.bin --crypt-password "secret" --output ./data.csv

# Decrypt a folder (output directory: ./dataset-encrypted-decrypted)
abc data decrypt ./dataset-encrypted --crypt-password "secret" --crypt-salt "pepper"
```

---

## `data download`

Submit a [nf-core/fetchngs](https://github.com/nf-core/fetchngs) pipeline run as a data download job on the cluster.

```
abc data download [flags]
```

### Flags

| Flag            | Description                                                      |
|-----------------|------------------------------------------------------------------|
| `--accession`   | Accession(s) to fetch (repeatable; e.g. SRR, ERR, DRR IDs)      |
| `--params-file` | Path to a YAML or JSON params file                               |
| `--name`        | Custom name for this download run                                |
| `--config`      | Path to a Nextflow config file                                   |
| `--profile`     | Nextflow profile(s) to use                                       |
| `--work-dir`    | Work directory for pipeline execution                            |
| `--revision`    | Pipeline revision (branch, tag, or commit SHA)                   |

At least one `--accession` or a `--params-file` is required.

### Examples

```bash
# Download a single accession
abc data download --accession SRR1234567

# Download multiple accessions
abc data download --accession SRR1234567 --accession SRR1234568

# Download using a params file
abc data download --params-file fetchngs-params.yaml

# Download with a custom Nextflow config and profile
abc data download \
  --accession SRR1234567 \
  --config custom.config \
  --profile test \
  --name my-download-run
```
