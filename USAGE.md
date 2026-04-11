# abc CLI — Command Reference

This document describes every command available in the `abc` CLI.

## Table of Contents

- [Global flags](#global-flags)
- [Elevation tiers](#elevation-tiers)
- [Debug logging](#debug-logging)
- [submit](#submit)
- [pipeline run](#pipeline-run)
- [pipeline lifecycle](#pipeline-lifecycle)
- [module run](#module-run)
- [job run](#job-run)
  - [Preamble directives](#preamble-directives)
  - [Directive precedence](#directive-precedence)
- [job list](#job-list)
- [job show](#job-show)
- [job stop](#job-stop)
- [job logs](#job-logs)
- [job status](#job-status)
- [job dispatch](#job-dispatch)
- [job translate](#job-translate)
- [logs (alias)](#logs-alias)
- [data upload](#data-upload)
- [data encrypt](#data-encrypt)
- [data decrypt](#data-decrypt)
- [data download](#data-download)
- [node add](#node-add)
- [storage size](#storage-size)
- [namespace](#namespace)
- [cluster](#cluster)
- [budget](#budget)
- [service](#service)
- [status (alias)](#status-alias)

---

## Global flags

These flags are available on every `abc` command.

| Flag             | Env var              | Description                                      | Default                      |
|------------------|----------------------|--------------------------------------------------|------------------------------|
| `--url`          | `ABC_API_ENDPOINT`   | abc-cluster API endpoint URL                     | `https://api.abc-cluster.io` |
| `--access-token` | `ABC_ACCESS_TOKEN`   | abc-cluster access token                         | *(unset)*                    |
| `--workspace`    | `ABC_WORKSPACE_ID`   | Workspace ID                                     | *(user's default workspace)* |
| `--cluster`      | `ABC_CLUSTER`        | Target a specific named cluster in the fleet     | *(unset)*                    |
| `--quiet` / `-q` |                      | Suppress informational output to stderr          | `false`                      |
| `--debug[=N]`    | `ABC_DEBUG`          | Write structured JSON debug log (see [Debug logging](#debug-logging)) | `0` (off) |
| `--sudo`         |                      | Elevate to cluster-admin scope (required for namespace/node write ops) | `false` |
| `--cloud`        |                      | Elevate to infrastructure scope (required for cluster/budget write ops) | `false` |
| `--exp`          |                      | Enable experimental CLI features                 | `false`                      |

---

## Elevation tiers

`abc` uses three opt-in elevation flags that mirror Linux sudo semantics:

| Flag      | Scope               | Required for                                   |
|-----------|---------------------|------------------------------------------------|
| *(none)*  | User operations     | pipeline, job, data, module, submit            |
| `--sudo`  | Cluster-admin       | `namespace create/delete`, `node add/drain`    |
| `--cloud` | Infrastructure      | `cluster provision/decommission`, `budget set` |
| `--exp`   | Experimental        | Community task drivers, unreleased features    |

---

## Debug logging

`--debug[=N]` writes a structured JSON-Lines log file containing every significant event
(SSH dials, preflight checks, commands run, uploads, downloads, Nomad API calls, errors).
Pass it to an AI model or `jq` for diagnosis.

| Level | Flag | What is logged |
|-------|------|----------------|
| `0` | *(omitted)* | Nothing — noop handler, zero overhead |
| `1` | `--debug` or `--debug=1` | All operation events: SSH dial, host key, auth method, preflight checks, uploads, downloads, service ops, errors with full chain. Good for AI diagnosis. |
| `2` | `--debug=2` | L1 + exact remote commands (redacted), SSH session lifecycle, Nomad HTTP request/response |
| `3` | `--debug=3` | L2 + SSH protocol detail, raw preflight stdout/stderr, full HCL content |

**Log file location:**

| Platform | Path |
|----------|------|
| macOS    | `~/Library/Logs/abc-cluster-cli/debug-<timestamp>.log` |
| Linux    | `~/.local/share/abc-cluster-cli/logs/debug-<timestamp>.log` |
| Fallback | `~/.abc/logs/debug-<timestamp>.log` |

File permissions: `0600`. Sensitive data (passwords, tokens, private keys) is **always redacted** — the log is safe to share.

```bash
# Default debug level (recommended for issue reports)
abc --debug node add --host 10.0.0.5

# Verbose — includes remote commands
abc --debug=2 node add --host 10.0.0.5

# Via environment variable
ABC_DEBUG=1 abc node add --host 10.0.0.5
```

On failure, the CLI prints:
```
[abc debug] operation failed — attach the log above when reporting issues
```

---

## `submit`

Unified entry point. Auto-detects whether `<target>` is a Nextflow pipeline, an nf-core module,
or a local batch script and dispatches to the appropriate underlying command.

```
abc submit <target> [flags]
```

### Detection order

| Priority | Condition | Dispatches to |
|----------|-----------|---------------|
| 1 | `--type pipeline\|job\|module` | forced |
| 2 | `--conda <spec>` or `--pixi` | `job run` with auto-generated wrapper |
| 3 | `<target>` is a local file path | `job run --submit` |
| 4 | `<target>` starts with `http://` or `https://` | `pipeline run` |
| 5 | `<target>` has ≥ 3 path segments (e.g. `nf-core/modules/bwa/mem`) | `module run` |
| 6 | `<target>` matches `owner/repo` (one `/`) | `pipeline run` |
| 7 | `<target>` matches a saved pipeline name in Nomad Variables | `pipeline run` |
| — | no match | error — use `--type` |

### Flags

**Data / params**

| Flag | Description |
|------|-------------|
| `--input <path>` | Input file/samplesheet/directory (→ `params.input`) |
| `--output <path>` | Output directory (→ `params.outdir`; nf-core convention) |
| `--param key=val` | Extra parameter (repeatable; merged into params file) |

**Mode**

| Flag | Description |
|------|-------------|
| `--type pipeline\|job\|module` | Force dispatch mode; bypass auto-detection |

**Pipeline flags** *(active when mode = pipeline)*

| Flag | Description |
|------|-------------|
| `--revision <string>` | Git branch/tag/SHA |
| `--profile <string>` | Nextflow profile(s), comma-separated |
| `--config <path>` | Extra Nextflow config file to merge |
| `--work-dir <path>` | Nextflow work directory |
| `--nf-version <string>` | Nextflow Docker image tag |

**Conda / job flags** *(active when mode = job with a wrapper)*

| Flag | Default | Description |
|------|---------|-------------|
| `--conda <spec>` | | Conda package spec; triggers conda wrapper mode |
| `--conda-solver <name>` | `conda` | Solver used to activate the env: `conda`, `mamba`, or `micromamba` |
| `--pixi` | | Run `<target>` via `pixi run`; triggers pixi wrapper mode |
| `--cores <int>` | | CPU cores |
| `--mem <size>` | | Memory, e.g. `4G`, `512M` |
| `--time <HH:MM:SS>` | | Walltime limit |
| `--tool-arg <string>` | | Extra arg appended to the tool invocation (repeatable; conda/pixi modes) |

**Shared**

| Flag | Description |
|------|-------------|
| `--name <string>` | Override Nomad job name |
| `--namespace <string>` | Nomad namespace |
| `--datacenter <string>` | Nomad datacenter (repeatable) |
| `--wait` | Block until job completes |
| `--logs` | Stream logs after submit |
| `--dry-run` | Print generated HCL without submitting |

### Examples

```bash
# Run a saved pipeline with a samplesheet
abc submit rnaseq --input samplesheet.csv

# Run an nf-core pipeline directly
abc submit nf-core/rnaseq --input samplesheet.csv --revision 3.14 --output /results

# Run an nf-core module
abc submit nf-core/modules/bwa/mem --input samplesheet.csv

# Submit a local script with input data
abc submit align.sh --input /data/reads

# Run a conda tool (auto-generates wrapper using conda)
abc submit fastqc --conda fastqc --input /data/reads --output /results

# Use mamba as the solver instead of conda
abc submit fastqc --conda fastqc --conda-solver mamba --input /data/reads

# Use micromamba
abc submit fastqc --conda fastqc --conda-solver micromamba --input /data/reads

# Run a pixi task
abc submit fastqc --pixi --input /data/reads --output /results

# Force pipeline mode and stream logs
abc submit my-analysis --type pipeline --wait --logs
```

---

## `pipeline run`

Submit a Nextflow pipeline as a head job on Nomad. The positional argument is either a saved
pipeline name (stored in Nomad Variables via `pipeline add`) or a GitHub/GitLab URL for an
ad-hoc run.

```
abc pipeline run <name-or-url> [flags]
```

### Flags

| Flag                  | Description                                                         | Default |
|-----------------------|---------------------------------------------------------------------|---------|
| `--params-file`       | YAML or JSON file with Nextflow pipeline parameters                 |         |
| `--revision`          | Pipeline revision (branch, tag, or commit SHA)                      |         |
| `--profile`           | Nextflow config profile(s), comma-separated                         |         |
| `--config`            | Extra Nextflow config file to merge into the run                    |         |
| `--work-dir`          | Shared host volume path for Nextflow work directory                 | `/work/nextflow-work` |
| `--datacenter`        | Nomad datacenter (repeatable)                                       | `dc1`   |
| `--nf-version`        | Nextflow Docker image tag                                           | `25.10.4` |
| `--nf-plugin-version` | nf-nomad plugin version                                             | `0.4.0-edge3` |
| `--cpu`               | Head job CPU in MHz                                                 | `1000`  |
| `--memory`            | Head job memory in MB                                               | `2048`  |
| `--name`              | Override Nomad job name                                             | `nextflow-head` |
| `--wait`              | Block until the head job completes                                  |         |
| `--logs`              | Stream head job logs after submit                                   |         |
| `--dry-run`           | Print generated HCL without submitting                              |         |

### Examples

```bash
# Run a saved pipeline by name
abc pipeline run rnaseq --params-file params.yaml

# Ad-hoc run from GitHub
abc pipeline run https://github.com/nf-core/rnaseq --revision 3.14

# Override resources for a large run
abc pipeline run nf-core/rnaseq \
  --params-file params.yaml \
  --cpu 2000 --memory 8192 \
  --profile test,docker \
  --wait
```

---

## Pipeline lifecycle

Pipelines are stored as JSON in Nomad Variables at `nomad/pipelines/<name>` and can be versioned,
exported, and imported.

### `pipeline add <repository>`

Save a pipeline configuration to the cluster.

```
abc pipeline add <repository> --name <name> [flags]
```

| Flag                  | Description                                              |
|-----------------------|----------------------------------------------------------|
| `--name`              | Pipeline name (**required**)                             |
| `--description`       | Human-readable description                               |
| `--revision`          | Default git revision                                     |
| `--profile`           | Default Nextflow profile(s), comma-separated             |
| `--work-dir`          | Default work directory                                   |
| `--config`            | Default extra Nextflow config file                       |
| `--params-file`       | Default pipeline parameters (YAML/JSON)                  |
| `--nf-version`        | Default Nextflow Docker image tag                        |
| `--nf-plugin-version` | Default nf-nomad plugin version                          |
| `--cpu`               | Default head job CPU in MHz                              |
| `--memory`            | Default head job memory in MB                            |
| `--datacenter`        | Default Nomad datacenter(s) (repeatable)                 |

```bash
abc pipeline add https://github.com/nf-core/rnaseq \
  --name rnaseq \
  --revision 3.14 \
  --profile test,docker
```

### `pipeline list`

List all saved pipelines.

```bash
abc pipeline list
```

### `pipeline info <name>`

Show full details of a saved pipeline, including all stored defaults.

```bash
abc pipeline info rnaseq
abc pipeline info rnaseq --json
```

### `pipeline update <name>`

Update the default configuration of a saved pipeline. Only flags that are explicitly provided
are changed; omitted flags keep their existing value.

```
abc pipeline update <name> [flags]
```

Accepts the same flags as `pipeline add` (except `--name`).

```bash
abc pipeline update rnaseq --revision 3.15
abc pipeline update rnaseq --cpu 2000 --memory 4096
```

### `pipeline delete <name>`

Remove a saved pipeline from the cluster.

```bash
abc pipeline delete rnaseq
abc pipeline delete rnaseq --yes   # skip confirmation
```

### `pipeline export <name> [output-file]`

Export a saved pipeline configuration to YAML. Useful for version control and cluster migration.

```bash
abc pipeline export rnaseq              # prints YAML to stdout
abc pipeline export rnaseq rnaseq.yaml  # writes to file
```

### `pipeline import <file>`

Import a pipeline configuration from a YAML file.

```bash
abc pipeline import rnaseq.yaml
abc pipeline import rnaseq.yaml --name rnaseq-v2   # override name
```

---

## `module run`

Generate and run an nf-core module driver pipeline using
[nf-pipeline-gen](https://github.com/abc-cluster/nf-pipeline-gen) as a two-phase Nomad batch job.

**Phase 1 (prestart task):** Downloads the nf-pipeline-gen release binary, fetches the nf-core/modules
repository, and generates a minimal Nextflow driver pipeline for the requested module.

**Phase 2 (main task):** Runs the generated driver with Nextflow on the cluster.

```
abc module run <nf-core/module> [flags]
```

### Flags

| Flag                    | Description                                                              | Default |
|-------------------------|--------------------------------------------------------------------------|---------|
| `--name`                | Override Nomad job name                                                   | `module-<slug>` |
| `--profile`             | Nextflow profile(s) for the generated driver run                          | `nomad,test` |
| `--work-dir`            | Shared host volume path                                                   | `/work/nextflow-work` |
| `--output-prefix`       | Output prefix for generated module runs                                   | `s3://user-output/nextflow` |
| `--params-file`         | Optional params YAML to pass to nf-pipeline-gen                           |         |
| `--config-file`         | Optional module.config for nf-pipeline-gen                                |         |
| `--module-revision`     | Override module revision recorded in generated driver                     |         |
| `--pipeline-gen-repo`   | GitHub repository for nf-pipeline-gen release assets (`owner/repo`)       | `abc-cluster/nf-pipeline-gen` |
| `--pipeline-gen-version`| nf-pipeline-gen release version                                           | `latest` |
| `--github-token`        | GitHub token for release API/download access (or `GITHUB_TOKEN`/`GH_TOKEN`) |       |
| `--nf-version`          | Nextflow Docker image tag                                                 | `25.10.4` |
| `--nf-plugin-version`   | nf-nomad plugin version for execution config                              | `0.4.0-edge3` |
| `--cpu`                 | Main Nextflow task CPU in MHz                                             | `1500`  |
| `--memory`              | Main Nextflow task memory in MB                                           | `4096`  |
| `--datacenter`          | Nomad datacenter(s) (repeatable)                                          | `dc1`   |
| `--minio-endpoint`      | Optional MinIO endpoint for generated driver execution                    |         |
| `--wait`                | Block until the module run job completes                                  |         |
| `--logs`                | Stream module run logs after submit                                       |         |
| `--dry-run`             | Print generated HCL without submitting                                    |         |

### Examples

```bash
# Run the bwa/mem module
abc module run nf-core/modules/bwa/mem

# Run with a test profile and wait for completion
abc module run nf-core/modules/fastqc --profile nomad,test --wait

# Use a specific nf-pipeline-gen version
abc module run nf-core/modules/samtools/sort \
  --pipeline-gen-version v0.3.0 \
  --output-prefix s3://my-bucket/results

# Dry-run to inspect generated HCL
abc module run nf-core/modules/bwa/mem --dry-run
```

---

## `job run`

Parse `#ABC`/`#NOMAD` preamble directives from an annotated shell script and produce a Nomad HCL
batch job spec. Without `--submit` the generated HCL is printed to stdout; with `--submit` it is
registered directly with the Nomad server.

```
abc job run <script> [flags]
```

### Submission flags

| Flag            | Description                                                  |
|-----------------|--------------------------------------------------------------|
| `--submit`      | Submit the job to Nomad instead of printing HCL              |
| `--dry-run`     | Plan the job server-side without submitting                  |
| `--watch`       | Stream logs immediately after `--submit`                     |
| `--output-file` | Write generated HCL to a file instead of stdout             |

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
| `--hpc-compat-env`              | `--hpc_compat_env`                  | Inject legacy `SLURM_*` / `PBS_*` compatibility aliases           |
| `--no-network`                  | `--no-network`                      | Disable network access (Nomad mode = `"none"`)                    |
| `--port`                        | `--port=<label>`                    | Named dynamic port; injects `NOMAD_IP/PORT/ADDR_<label>`          |
| `--constraint=<attr><op><val>`  | `--constraint=<attr><op><val>`      | Nomad placement constraint (repeatable). Ops: `== != =~ !~ < <= > >=` |
| `--affinity=<expr>[,weight=N]`  | `--affinity=<expr>[,weight=N]`      | Nomad placement affinity (repeatable)                             |
| *(preamble only)*               | `--driver.config.<key>=<val>`       | Arbitrary driver config field                                     |

#### Reschedule flags

| Flag                    | Preamble directive                  | Description                               |
|-------------------------|-------------------------------------|-------------------------------------------|
| `--reschedule-mode`     | `--reschedule-mode=<delay\|fail>`   | Reschedule policy mode                    |
| `--reschedule-attempts` | `--reschedule-attempts=<int>`       | Maximum reschedule attempts               |
| `--reschedule-interval` | `--reschedule-interval=<duration>`  | Reschedule evaluation window (e.g. `30s`) |
| `--reschedule-delay`    | `--reschedule-delay=<duration>`     | Base reschedule delay (e.g. `5s`)         |
| `--reschedule-max-delay`| `--reschedule-max-delay=<duration>` | Maximum reschedule delay (e.g. `1m`)      |

### Runtime-exposure flags (Class 2)

These preamble directives inject the corresponding `NOMAD_*` variable into the task's environment
block so the script can read the value at execution time.

#### Task identity

| Preamble directive  | Env var injected       | Notes                                    |
|---------------------|------------------------|------------------------------------------|
| `--hpc_compat_env`  | `SLURM_*`, `PBS_*` aliases | Opt-in migration shim for legacy scripts |
| `--alloc_id`        | `NOMAD_ALLOC_ID`       | Unique per execution                     |
| `--short_alloc_id`  | `NOMAD_SHORT_ALLOC_ID` |                                          |
| `--alloc_name`      | `NOMAD_ALLOC_NAME`     | `<job>.<group>[<index>]`                 |
| `--alloc_index`     | `NOMAD_ALLOC_INDEX`    | 0-based; use to shard array jobs         |
| `--job_id`          | `NOMAD_JOB_ID`         |                                          |
| `--job_name`        | `NOMAD_JOB_NAME`       |                                          |
| `--parent_job_id`   | `NOMAD_JOB_PARENT_ID`  | Dispatched jobs only                     |
| `--group_name`      | `NOMAD_GROUP_NAME`     |                                          |
| `--task_name`       | `NOMAD_TASK_NAME`      |                                          |
| `--namespace`       | `NOMAD_NAMESPACE`      | Use without `=<value>` to expose env only|
| `--dc`              | `NOMAD_DC`             | Use without `=<value>` to expose env only|

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

| Flag / Directive       | Description                                                          |
|------------------------|----------------------------------------------------------------------|
| `--meta=<key>=<value>` | Nomad meta block entry (repeatable). Key is uppercased for env access as `NOMAD_META_<KEY>`. |

### Params file

| Flag            | Description                                                        |
|-----------------|--------------------------------------------------------------------|
| `--params-file` | YAML file with directive key/value pairs (lowest priority after env vars). Nested keys are dot-flattened: `cores: 8` → `--cores=8`. |

### Nomad connection flags

| Flag            | Env var                       | Description             | Default                 |
|-----------------|-------------------------------|-------------------------|-------------------------|
| `--nomad-addr`  | `ABC_ADDR` / `NOMAD_ADDR`     | Nomad API address       | `http://127.0.0.1:4646` |
| `--nomad-token` | `ABC_TOKEN` / `NOMAD_TOKEN`   | Nomad ACL token         | *(unset)*               |
| `--region`      | `ABC_REGION` / `NOMAD_REGION` | Nomad region            | *(unset)*               |

### Directive precedence

From highest to lowest priority:

```
CLI flags  >  #ABC preamble  >  #NOMAD preamble  >  NOMAD_* env vars  >  params file
```

### Preamble directives

Scripts can include a block of `#ABC` or `#NOMAD` comment directives before the first
non-comment line. Both prefix styles accept the same directive keys. `#SBATCH` directives are
also understood (mapped to their ABC equivalents) for SLURM script compatibility.

```bash
#ABC --cores=8    # 8 cores per task (inline comments are stripped)
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

| Flag          | Description                                              | Default |
|---------------|----------------------------------------------------------|---------|
| `--status`    | Filter by status: `running`, `complete`, `dead`, `pending` | *(all)* |
| `--region`    | Filter by Nomad region                                   | *(all)* |
| `--namespace` | Filter by namespace                                      | *(all)* |
| `--limit`     | Maximum number of results to show                        | `20`    |

```bash
abc job list --status running
abc job list --namespace my-ns --limit 50
```

---

## `job show`

Show detailed information about a Nomad batch job, including task groups and recent allocations.

```
abc job show <job-id> [flags]
```

| Flag          | Description     |
|---------------|-----------------|
| `--namespace` | Nomad namespace |

```bash
abc job show script-job-bwa-align-a1b2c3d4
```

---

## `job stop`

Stop a running Nomad batch job.

```
abc job stop <job-id> [flags]
```

| Flag          | Description                                            |
|---------------|--------------------------------------------------------|
| `--purge`     | Remove the job definition from Nomad after stopping    |
| `--detach`    | Return immediately without waiting for the stop        |
| `--yes`       | Skip the confirmation prompt                           |
| `--namespace` | Nomad namespace                                        |

```bash
abc job stop script-job-bwa-align-a1b2c3d4 --purge --yes
```

---

## `job logs`

Stream or print logs for a Nomad batch job.

```
abc job logs <job-id> [flags]
```

| Flag          | Short | Description                                              |
|---------------|-------|----------------------------------------------------------|
| `--follow`    | `-f`  | Stream logs in real time                                 |
| `--alloc`     |       | Filter to a specific allocation ID prefix                |
| `--task`      |       | Task name within the allocation (default: `main`)        |
| `--type`      |       | Log stream: `stdout` or `stderr` (default: `stdout`)     |
| `--namespace` |       | Nomad namespace                                          |
| `--since`     |       | Show logs since this timestamp (RFC3339)                 |
| `--output`    |       | Write stdout logs to a file                              |
| `--error`     |       | Write stderr logs to a file                              |

```bash
abc job logs script-job-bwa-align-a1b2c3d4 --follow
abc job logs my-job --type stderr --alloc a1b2c3d4
abc job logs my-job --output job-output.txt
```

---

## `job status`

Print a compact one-line status summary for a Nomad batch job and exit with a machine-readable code.

```
abc job status <job-id> [flags]
```

| Exit code | Meaning                               |
|-----------|---------------------------------------|
| `0`       | Job complete with no failures         |
| `1`       | Job dead or failed                    |
| `2`       | Job still running or pending          |
| `3`       | Error reaching Nomad or job not found |

| Flag          | Description     |
|---------------|-----------------|
| `--namespace` | Nomad namespace |

```bash
abc job status script-job-bwa-align-a1b2c3d4
echo "Exit: $?"
```

---

## `job dispatch`

Dispatch an instance of a parameterized Nomad batch job.

```
abc job dispatch <job-id> [flags]
```

| Flag       | Description                                                          |
|------------|----------------------------------------------------------------------|
| `--meta`   | Meta key=value pair to pass to the dispatched job (repeatable)       |
| `--input`  | Path to a file whose contents are passed as the dispatch payload     |
| `--detach` | Do not wait for the dispatched allocation to start                   |

```bash
abc job dispatch my-parameterized-job \
  --meta sample_id=S001 \
  --meta lane=L001 \
  --input payload.bin
```

---

## `job translate`

Translate a SLURM or PBS job script to `#ABC` directives. Output is a shell script with
`#ABC`/`#NOMAD` preamble replacing the original scheduler directives — it is **not** HCL.

```
abc job translate <script> [flags]
```

| Flag         | Description                                            |
|--------------|--------------------------------------------------------|
| `--out`      | Write translated script to file (default: stdout)      |
| `--strict`   | Fail when an unmapped directive is found               |
| `--executor` | Force scheduler type: `slurm` or `pbs`                 |

```bash
# Translate a SLURM script and preview
abc job translate slurm-job.sh

# Translate and write to a new file
abc job translate slurm-job.sh --out abc-job.sh

# Strict mode — fail on unmapped directives
abc job translate slurm-job.sh --strict
```

---

## `logs` (alias)

Top-level alias for `abc job logs`. Accepts all the same flags.

```
abc logs <job-id> [flags]
```

---

## `data upload`

Upload a local file or folder using the tus resumable upload protocol.

```
abc data upload <path> [flags]
```

| Flag               | Env var               | Description                                                         |
|--------------------|-----------------------|---------------------------------------------------------------------|
| `--name`           |                       | Display name for the uploaded file                                  |
| `--endpoint`       | `ABC_UPLOAD_ENDPOINT` | Tus endpoint URL (default: `<url>/data/uploads`)                    |
| `--upload-token`   | `ABC_UPLOAD_TOKEN`    | Bearer token for tus (falls back to `--access-token`)               |
| `--crypt-password` |                       | rclone crypt password for client-side encryption                    |
| `--crypt-salt`     |                       | rclone crypt salt (password2)                                       |
| `--checksum`       |                       | Include SHA-256 checksum metadata (default: `true`)                 |
| `--progress`       |                       | Show live progress bars (default: `true`)                           |
| `--parallel`       |                       | Upload directory files in parallel (default: `true`)                |
| `--parallel-jobs`  |                       | Number of parallel workers (default: CPU count)                     |
| `--chunk-size`     |                       | Upload chunk size (e.g. `64MB`; default: `64MB`)                    |
| `--max-rate`       |                       | Maximum upload throughput (e.g. `50MB/s`); unlimited by default     |
| `--meta`           |                       | Extra tus metadata as `key=value` (repeatable)                      |
| `--no-resume`      |                       | Ignore stored resume state; always start a fresh upload             |
| `--status`         |                       | Show stored resume state (does not upload)                          |
| `--clear`          |                       | Clear stored resume state (does not upload)                         |

```bash
abc data upload ./data.csv
abc data upload ./dataset                             # recursive, parallel
abc data upload ./data.csv --crypt-password "secret"
abc data upload ./data.csv --status
abc data upload ./data.csv --clear && abc data upload ./data.csv --no-resume
```

---

## `data encrypt`

Encrypt a file or folder with rclone crypt format.

```
abc data encrypt <path> [flags]
```

| Flag               | Description                                          |
|--------------------|------------------------------------------------------|
| `--crypt-password` | rclone crypt password (**required**)                 |
| `--crypt-salt`     | rclone crypt salt (password2)                        |
| `--output`         | Output file path (single-file encryption)            |
| `--output-dir`     | Output directory (folder encryption)                 |
| `--progress`       | Show live progress bars (default: `true`)            |

```bash
abc data encrypt ./data.csv --crypt-password "secret"
abc data encrypt ./dataset --crypt-password "secret" --crypt-salt "pepper"
```

---

## `data decrypt`

Decrypt a file or folder previously encrypted with `abc data encrypt` or rclone crypt.

```
abc data decrypt <path> [flags]
```

| Flag               | Description                                          |
|--------------------|------------------------------------------------------|
| `--crypt-password` | rclone crypt password (**required**)                 |
| `--crypt-salt`     | rclone crypt salt (password2)                        |
| `--output`         | Output file path (single-file decryption)            |
| `--output-dir`     | Output directory (folder decryption)                 |

```bash
abc data decrypt ./data.csv.bin --crypt-password "secret" --output ./data.csv
```

---

## `data download`

Submit an [nf-core/fetchngs](https://github.com/nf-core/fetchngs) pipeline run as a data
download job on the cluster.

```
abc data download [flags]
```

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

```bash
abc data download --accession SRR1234567
abc data download --accession SRR1234567 --accession SRR1234568
abc data download --params-file fetchngs-params.yaml
```

---

## `node add`

Add a compute node to the cluster. Runs preflight checks, installs Nomad (and optionally
Tailscale and community task drivers), and registers the node.

Requires `--sudo`.

```
abc node add [flags]
```

### Transport mode (one required)

| Flag              | Description                                                           |
|-------------------|-----------------------------------------------------------------------|
| `--local`         | Install on the current machine                                        |
| `--host <ip>`     | Install on a remote machine via SSH                                   |
| `--cloud`         | Provision a cloud VM (requires `--cloud` elevation)                   |

### SSH flags (`--host` mode)

| Flag                    | Description                                                     | Default |
|-------------------------|-----------------------------------------------------------------|---------|
| `--user`                | SSH user                                                        | current OS user |
| `--password`            | Login password for SSH auth and sudo (also `ABC_NODE_PASSWORD`) |         |
| `--ssh-key`             | SSH private key path                                            | `~/.ssh/id_rsa`, then SSH agent |
| `--ssh-port`            | SSH port                                                        | `22`    |
| `--skip-host-key-check` | Disable known_hosts verification (insecure; dev only)           | `false` |
| `--jump-host`           | SSH jump/bastion host (equivalent to `ssh -J`)                  |         |
| `--jump-user`           | Username on the jump host                                       | same as `--user` |
| `--jump-port`           | SSH port on the jump host                                       | `22`    |
| `--jump-key`            | SSH private key for the jump host                               | same as `--ssh-key` |
| `--print-commands`      | Print a self-contained install script instead of executing      | `false` |
| `--target-os`           | Target OS/arch for `--print-commands` with `--host` (e.g. `linux/amd64`) | `linux/amd64` |

The SSH auth chain (highest to lowest priority): explicit `--ssh-key` → default key files → SSH agent → keyboard-interactive → `--password`. When `--password` is provided it is also used transparently for `sudo -S` so the install proceeds without a second prompt.

Jump host support reads `~/.ssh/config` and resolves `ProxyJump` entries automatically.

### Nomad placement flags

| Flag                    | Description                                            | Default    |
|-------------------------|--------------------------------------------------------|------------|
| `--datacenter`          | Nomad datacenter label                                 | `default`  |
| `--node-class`          | Nomad node class label (optional)                      |            |
| `--server-join`         | Nomad server address(es) to join (repeatable)          |            |
| `--network-interface`   | Nomad client network interface                         | `tailscale0` when Tailscale is used |
| `--host-volume`         | Nomad host volume `name=path[:read_only]` (repeatable) |            |
| `--scratch-host-volume` | Configure default `scratch` host volume                | `true`     |
| `--scratch-host-volume-path` | Path for the scratch host volume                 | `/opt/nomad/scratch` |

### Tailscale flags

| Flag                           | Description                                                              | Default |
|--------------------------------|--------------------------------------------------------------------------|---------|
| `--tailscale`                  | Join the Tailscale tailnet during provisioning                           | `false` |
| `--tailscale-auth-key`         | Pre-auth key (auto-created if omitted and `TAILSCALE_API_KEY` is set)    |         |
| `--tailscale-hostname`         | Override Tailscale hostname                                              | OS hostname |
| `--tailscale-create-auth-key`  | Auto-create auth key via Tailscale API                                   | `true`  |
| `--tailscale-key-ephemeral`    | Register the node as ephemeral                                           | `true`  |
| `--tailscale-key-reusable`     | Make the auto-created auth key reusable                                  | `false` |
| `--tailscale-key-expiry`       | Auth key expiry (e.g. `30m`, `2h`, `24h`)                                | `24h`   |
| `--tailscale-key-preauthorized`| Mark devices as preauthorized                                            | `true`  |
| `--nomad-use-tailscale-ip`     | Set Nomad advertise address to the node's Tailscale IPv4                 | `false` |

### Community driver flags (requires `--exp`)

| Flag                         | Description                                                  | Default |
|------------------------------|--------------------------------------------------------------|---------|
| `--community-driver`         | Install community task driver(s): `containerd`, `exec2` (repeatable) | |
| `--local-driver`             | Deploy a local driver binary: `[name=]path` (repeatable)     |         |
| `--java-driver`              | Install JDK(s) and configure Nomad Java task driver          | `false` |
| `--jdk-version`              | JDK major versions to install (repeatable, e.g. `17`, `21`) |         |
| `--jdk-default-version`      | Default JDK version for `/usr/local/bin/java`                |         |

### Other flags

| Flag                    | Description                                            | Default    |
|-------------------------|--------------------------------------------------------|------------|
| `--nomad-version`       | Nomad version to install                               | latest stable |
| `--package-install-method` | `static` (download binary) or `package-manager`    | `static`   |
| `--encrypt`             | Nomad gossip encryption key                            |            |
| `--acl`                 | Enable Nomad ACL system on this node                   | `false`    |
| `--skip-preflight`      | Skip OS compatibility checks                           | `false`    |
| `--skip-enable`         | Install binary/config but do not enable the service    | `false`    |
| `--skip-start`          | Enable service but do not start it immediately         | `false`    |
| `--dry-run`             | Print what would be executed without making changes    | `false`    |

### Preflight checks

Before installing, `node add` validates:

| Check | Hard stop if fails |
|-------|--------------------|
| OS detection (Linux/macOS) | Yes — unsupported OS |
| Init system (systemd/launchd) | Yes on Linux — required for service management |
| Sudo access | Yes — required for install |
| Package manager (apt/dnf/brew) | Warning only (static install still works) |
| Nomad already installed | Info only — version will be updated |
| Tailscale already connected | Info only |

### Examples

```bash
# Install on the current machine
abc --sudo node add --local --server-join 10.0.0.1:4647

# Install on a remote machine via SSH
abc --sudo node add --host 10.0.0.5 --user ubuntu --server-join 10.0.0.1:4647

# Install with password auth (no SSH key)
abc --sudo node add --host 10.0.0.5 --user ubuntu --password mypassword \
  --server-join 10.0.0.1:4647

# Install via a bastion host
abc --sudo node add --host 10.0.0.5 --user ubuntu \
  --jump-host bastion.example.com --jump-user ec2-user \
  --server-join 10.0.0.1:4647

# Install with Tailscale auto-key creation
abc --sudo node add --host 10.0.0.5 --user ubuntu \
  --tailscale --tailscale-key-ephemeral \
  --server-join 10.0.0.1:4647

# Generate a self-contained install script (no execution)
abc --sudo node add --host 10.0.0.5 --print-commands > install.sh

# Install with community containerd driver (experimental)
abc --sudo --exp node add --host 10.0.0.5 --user ubuntu \
  --community-driver containerd \
  --server-join 10.0.0.1:4647

# Dry-run — show what would be executed
abc --sudo node add --host 10.0.0.5 --user ubuntu --dry-run
```

---

## `storage size`

Display storage size and usage for local server volumes and buckets.

```
abc storage size [flags]
```

| Flag          | Description                          |
|---------------|--------------------------------------|
| `--servers`   | Show server-local storage sizes      |
| `--buckets`   | Show bucket storage sizes            |
| `--all`       | Show all storage categories          |
| `--namespace` | Nomad namespace                      |

```bash
abc storage size --all
abc storage size --buckets
```

---

## `namespace`

Manage cluster namespaces. Read operations are available to all users; write operations require `--sudo`.

### `namespace list`

```bash
abc namespace list
```

### `namespace show <name>`

```bash
abc namespace show my-ns
```

### `namespace create` (requires `--sudo`)

```
abc --sudo namespace create [flags]
```

| Flag            | Description                                          |
|-----------------|------------------------------------------------------|
| `--name`        | Namespace name (**required**)                        |
| `--description` | Short description                                    |
| `--group`       | Research group or team name                          |
| `--contact`     | Contact email for the namespace owner                |
| `--priority`    | Default job priority for this namespace              |
| `--node-pool`   | Default node pool                                    |

```bash
abc --sudo namespace create --name team-alpha \
  --description "Alpha team namespace" \
  --group alpha --contact alpha@lab.org
```

### `namespace delete <name>` (requires `--sudo`)

```
abc --sudo namespace delete <name> [flags]
```

| Flag      | Description                                          |
|-----------|------------------------------------------------------|
| `--yes`   | Skip confirmation prompt                             |
| `--drain` | Stop all running jobs before deletion                |

```bash
abc --sudo namespace delete team-alpha --drain --yes
```

---

## `cluster`

Manage the cluster fleet. All cluster operations require `--cloud`.

### `cluster list` (requires `--cloud`)

```bash
abc --cloud cluster list
```

### `cluster status [name]` (requires `--cloud`)

```bash
abc --cloud cluster status
abc --cloud cluster status my-cluster
```

### `cluster provision` (requires `--cloud`)

Provision a new cluster.

```
abc --cloud cluster provision [flags]
```

| Flag              | Description                                         |
|-------------------|-----------------------------------------------------|
| `--name`          | Cluster name (**required**)                         |
| `--region`        | Cloud region (**required**)                         |
| `--size`          | Number of client nodes (default: `3`)               |
| `--node-type`     | VM instance type                                    |
| `--nomad-version` | Nomad version to install (default: latest)          |
| `--dry-run`       | Print the provisioning plan without creating resources |

```bash
abc --cloud cluster provision --name my-cluster --region za-cpt --size 5
```

### `cluster decommission <name>` (requires `--cloud`)

Drain and remove a cluster from the fleet.

```
abc --cloud cluster decommission <name> [flags]
```

| Flag         | Description                                          |
|--------------|------------------------------------------------------|
| `--yes`      | Skip confirmation prompt                             |
| `--drain`    | Drain all jobs before decommissioning (default: `true`) |
| `--deadline` | Maximum time to wait for drain (default: `2h`)       |

```bash
abc --cloud cluster decommission my-cluster --yes
```

---

## `budget`

View and manage namespace spend budgets. Read operations are available to all users;
`budget set` requires `--cloud`.

### `budget list` (requires `--cloud`)

```bash
abc --cloud budget list
```

### `budget show` (requires `--cloud`)

```
abc --cloud budget show [--namespace <name>]
```

### `budget set` (requires `--cloud`)

Set or update the spend cap for a namespace.

```
abc --cloud budget set [flags]
```

| Flag          | Description                                                   | Default |
|---------------|---------------------------------------------------------------|---------|
| `--namespace` | Namespace to configure (**required**)                         |         |
| `--monthly`   | Monthly spend cap in workspace currency (`0` = unlimited)     | `0`     |
| `--currency`  | Currency code (e.g. `USD`, `ZAR`, `EUR`)                      | `USD`   |
| `--alert-at`  | Alert threshold as a fraction of cap (0.0–1.0)               | `0.8`   |
| `--block-at`  | Submission block threshold as a fraction of cap (0.0–1.0)    | `1.0`   |

```bash
abc --cloud budget set --namespace team-alpha --monthly 500 --currency USD --alert-at 0.8
```

---

## `service`

Inspect backend service health and versions.

Valid service names: `nomad`, `jurist`, `minio`, `api`, `tus`, `cloud-gateway`

### `service ping <service>`

Check connectivity to a specific backend service.

```bash
abc service ping nomad
abc service ping minio
```

### `service version <service>`

Show the version of a specific backend service.

```bash
abc service version nomad
abc service version jurist
```

---

## `status` (alias)

Top-level alias — shows the health of all backend services at once.

```bash
abc status
```
