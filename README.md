# abc-cluster-cli

`abc` is the command line interface for the [abc-cluster](https://abc-cluster.io) platform, inspired by [tower-cli](https://github.com/seqeralabs/tower-cli).

It brings abc-cluster concepts like pipelines to the terminal, enabling you to launch and manage pipeline runs directly from your shell.

## Installation

```bash
go install github.com/abc-cluster/abc-cluster-cli@latest
```

Or build from source:

```bash
git clone https://github.com/abc-cluster/abc-cluster-cli.git
cd abc-cluster-cli
go build -o abc .
sudo mv abc /usr/local/bin/
```

## Configuration

You need an access token to interact with the abc-cluster API. Configure it in one of two ways:

- **Environment variable** (recommended):
  ```bash
  export ABC_ACCESS_TOKEN=<your-access-token>
  ```

- **Command flag**:
  ```bash
  abc --access-token=<your-access-token> <command>
  ```

Additional optional environment variables:

| Variable            | Description                                       | Default                        |
|---------------------|---------------------------------------------------|--------------------------------|
| `ABC_API_ENDPOINT`  | abc-cluster API URL                               | `https://api.abc-cluster.io`   |
| `ABC_WORKSPACE_ID`  | Workspace ID to use for operations                | *(user's default workspace)*   |

## Usage

### `pipeline run`

Submit a pipeline for execution on the abc-cluster platform.

```
abc pipeline run --pipeline <name-or-url> [flags]
```

**Flags:**

| Flag              | Short | Description                                              |
|-------------------|-------|----------------------------------------------------------|
| `--pipeline`      | `-p`  | Pipeline name or URL to run (**required**)               |
| `--name`          |       | Custom name for this run                                 |
| `--revision`      |       | Pipeline revision (branch, tag, or commit SHA)           |
| `--profile`       |       | Nextflow config profile(s) to use (comma-separated)      |
| `--work-dir`      |       | Work directory for pipeline execution                    |
| `--params-file`   |       | Path to a YAML or JSON file with pipeline parameters     |
| `--config`        |       | Path to a Nextflow config file to use for this run       |

**Global flags** (available on all commands):

| Flag              | Description                                                          |
|-------------------|----------------------------------------------------------------------|
| `--url`           | abc-cluster API endpoint URL (or set `ABC_API_ENDPOINT`)             |
| `--access-token`  | abc-cluster access token (or set `ABC_ACCESS_TOKEN`)                 |
| `--workspace`     | Workspace ID (or set `ABC_WORKSPACE_ID`)                             |

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

# Use a specific workspace and API endpoint
abc --url https://api.my-cluster.example.com \
    --workspace ws-12345 \
    pipeline run --pipeline my-pipeline
```

### `job run`

Generate a Nomad HCL batch job from an annotated Bash script and optionally pipe
it directly to Nomad.

```bash
abc job run <script> | nomad job run -
```

Scripts can include a preamble of `#ABC` or `#NOMAD` directives. `#ABC` entries
override `#NOMAD`, and both override NOMAD_* environment variables read at CLI
invocation.

**Supported directives:**

| Directive                          | Description |
|------------------------------------|-------------|
| `--name=<string>`                  | Job name |
| `--namespace=<string>`             | Nomad namespace |
| `--nodes=<int>`                    | Number of group instances (default: 1) |
| `--cores=<int>`                    | CPU cores reserved per task |
| `--mem=<size>[K|M|G]`              | Memory per task (KiB / MiB / GiB; stored as MiB) |
| `--gpus=<int>`                     | GPU count (nvidia/gpu device) |
| `--time=<HH:MM:SS>`                | Walltime limit (wrapped with the timeout command) |
| `--chdir=<path>`                   | Working directory inside the task sandbox |
| `--depend=<type:id>`               | Dependency on another job (injects a prestart task) |
| `--env=<NOMAD_VAR>[=<value>]`      | Emit a NOMAD_* runtime environment variable. If no value is provided, defaults to `${NOMAD_VAR}`. |

**Common NOMAD_* variables you can emit with `--env`:**

- Task identity: `NOMAD_ALLOC_ID`, `NOMAD_SHORT_ALLOC_ID`, `NOMAD_ALLOC_NAME`, `NOMAD_ALLOC_INDEX`
- Job identity: `NOMAD_JOB_NAME`, `NOMAD_JOB_ID`, `NOMAD_JOB_PARENT_ID`
- Task/group: `NOMAD_TASK_NAME`, `NOMAD_GROUP_NAME`
- Placement: `NOMAD_NAMESPACE`, `NOMAD_REGION`, `NOMAD_DC`
- Directories: `NOMAD_ALLOC_DIR`, `NOMAD_TASK_DIR`, `NOMAD_SECRETS_DIR`
- Resources: `NOMAD_CPU_LIMIT`, `NOMAD_CPU_CORES`, `NOMAD_MEMORY_LIMIT`, `NOMAD_MEMORY_MAX_LIMIT`
- Metadata: `NOMAD_META_<key>`
- Network patterns: `NOMAD_IP_<label>`, `NOMAD_PORT_<label>`, `NOMAD_ADDR_<label>`, `NOMAD_HOST_PORT_<label>`

For the full list of runtime environment variables, see the
[Nomad runtime environment settings](https://developer.hashicorp.com/nomad/docs/reference/runtime-environment-settings).

**Example script:**

```bash
#!/bin/bash
#ABC --name=ocean-model
#ABC --nodes=4
#ABC --cores=28
#ABC --mem=64G
#ABC --time=02:00:00
#ABC --env=NOMAD_ALLOC_ID
#ABC --env=NOMAD_TASK_DIR
#ABC --env=NOMAD_REGION=global
mpirun -np 112 ./ocean_model
```

### `data upload`

Upload a local file to the abc-cluster data service using tus resumable uploads.

```
abc data upload <file> [flags]
```

**Flags:**

| Flag         | Description                                                |
|--------------|------------------------------------------------------------|
| `--name`     | Display name for the uploaded file                         |
| `--endpoint` | Tus upload endpoint URL (defaults to `<url>/data/uploads`) |

### Examples

```bash
# Upload a file
abc data upload ./data.csv

# Upload with a display name
abc data upload ./data.csv --name sample-data
```

## Development

```bash
# Run tests
go test ./...

# Build
go build -o abc .
```
