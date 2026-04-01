# abc-cluster-cli

`abc` is the command line interface for the [abc-cluster](https://abc-cluster.io) platform, inspired by the smooth experience offered by the [tower-cli](https://github.com/seqeralabs/tower-cli).

It brings abc-cluster concepts like pipelines and ad-hoc Nomad jobs to the terminal, enabling you to launch and manage computations and automations directly from your shell.

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
| `ABC_UPLOAD_ENDPOINT` | Tus upload endpoint used by `abc data upload` (falls back to `<url>/data/uploads`) | *(unset)* |
| `ABC_UPLOAD_TOKEN`  | Bearer token used by `abc data upload` for tus auth (falls back to `ABC_ACCESS_TOKEN`) | *(unset)* |

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
it directly to Nomad. Supports preamble directives, params file, environment
overrides, and submission with watch options.

Usage:

```bash
# render HCL and print
abc job run myjob.sh

# submit to configured Nomad and wait up to 5m
abc job run myjob.sh --submit --watch

# custom watch timing
abc job run myjob.sh --submit --watch --watch-delay 20s --watch-timeout 3m

# use YAML params file
abc job run myjob.sh --params-file job-params.yaml --submit
```

Allowed preamble directives include:
- `#ABC --name=<name>`
- `#ABC --nodes=<n>`
- `#ABC --cores=<n>`
- `#ABC --mem=<size>`
- `#ABC --time=<HH:MM:SS>`
- `#ABC --namespace=<ns>`
- `#ABC --dc=<datacenter>`
- `#ABC --driver=<exec|docker|...>`
- `#ABC --output=<file>` (redirect stdout to `${NOMAD_TASK_DIR}/<file>`)
- `#ABC --error=<file>` (redirect stderr to `${NOMAD_TASK_DIR}/<file>`)
- `#ABC --reschedule-mode=<delay|fail>` and related reschedule fields

---

### `data upload`

Upload a local file or folder to the abc-cluster data service using tus resumable uploads.

Usage:

```bash
# upload a file with default endpoint + token
abc data upload ./local-data.tar.gz

# upload with explicit endpoint / token
ABC_UPLOAD_ENDPOINT=https://dev.abc-cluster.cloud/files \
  ABC_UPLOAD_TOKEN=abctoken \
  abc data upload ./local-data.tar.gz

# upload with advanced options
abc data upload ./local-data.tar.gz --name sample-data --crypt-password secret --crypt-salt pepper
```

Flags:

- `--endpoint`: customer tus endpoint URL (`ABC_UPLOAD_ENDPOINT` fallback)
- `--upload-token`: token for upload (`ABC_UPLOAD_TOKEN` fallback)
- `--name`: display name for the upload
- `--crypt-password`, `--crypt-salt`: optional client-side encrypt

---

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
| `--driver=<string>`                | Nomad task driver (default: exec2) |
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

Upload a local file or folder to the abc-cluster data service using tus resumable uploads.

```
abc data upload <path> [flags]
```

**Flags:**

| Flag         | Description                                                |
|--------------|------------------------------------------------------------|
| `--name`     | Display name for the uploaded file                         |
| `--endpoint` | Tus upload endpoint URL (or set `ABC_UPLOAD_ENDPOINT`; defaults to `<url>/data/uploads`) |
| `--crypt-password` | rclone crypt password for client-side encryption     |
| `--crypt-salt`     | rclone crypt salt (password2) for encryption          |
| `--upload-token` | Bearer token for tus uploads (or set `ABC_UPLOAD_TOKEN`; falls back to `--access-token`) |

`abc data upload` normalizes the endpoint to include a trailing slash if omitted.


### Examples

```bash
# Upload a file
abc data upload ./data.csv

# Upload using a dedicated tus bearer token
ABC_UPLOAD_TOKEN=<tusd-bearer-token> abc data upload ./data.csv

# Upload using a dedicated endpoint from env
ABC_UPLOAD_ENDPOINT=https://dev.abc-cluster.cloud/files abc data upload ./data.csv

# Upload with a display name
abc data upload ./data.csv --name sample-data

# Encrypt and upload with rclone-compatible crypt
abc data upload ./data.csv --crypt-password "secret" --crypt-salt "pepper"

# Upload all files from a folder (recursively)
abc data upload ./dataset
```

### `data encrypt`

Encrypt a local file or folder using the rclone crypt format so it can be uploaded later.

```
abc data encrypt <path> [flags]
```

**Flags:**

| Flag             | Description                                              |
|------------------|----------------------------------------------------------|
| `--output`       | Output file path for single-file encryption              |
| `--output-dir`   | Output directory for folder encryption                   |
| `--crypt-password` | rclone crypt password for client-side encryption      |
| `--crypt-salt`     | rclone crypt salt (password2) for encryption           |

### Examples

```bash
# Encrypt a single file to <file>.encrypted
abc data encrypt ./data.csv --crypt-password "secret"

# Encrypt a folder to ./dataset-encrypted
abc data encrypt ./dataset --crypt-password "secret" --crypt-salt "pepper"

# Upload a previously encrypted file as-is
abc data upload ./data.csv.encrypted
```

### `data decrypt`

Decrypt a local file or folder previously encrypted with `abc data encrypt`.

```
abc data decrypt <path> [flags]
```

**Flags:**

| Flag             | Description                                              |
|------------------|----------------------------------------------------------|
| `--output`       | Output file path for single-file decryption              |
| `--output-dir`   | Output directory for folder decryption                   |
| `--crypt-password` | rclone crypt password for client-side decryption      |
| `--crypt-salt`     | rclone crypt salt (password2) for decryption           |

### Examples

```bash
# Decrypt a single file
abc data decrypt ./data.csv.encrypted --crypt-password "secret"

# Decrypt a folder to ./dataset-decrypted
abc data decrypt ./dataset-encrypted --crypt-password "secret" --crypt-salt "pepper"
```

## Development

```bash
# Run tests
go test ./...

# Build
go build -o abc .

# Build multi-platform binaries locally (linux/darwin/windows x amd64/arm64)
bash scripts/local-matrix-build.sh
```

### Local tus + MinIO testing (rclone compatibility)

Use the provided docker compose setup to run a tusd server backed by MinIO:

```bash
docker compose -f docker-compose.tus-minio.yml up -d
```

Upload a file with client-side encryption:

```bash
abc data upload ./data.csv \
  --endpoint http://localhost:1080/files/ \
  --crypt-password "secret" \
  --crypt-salt "pepper"
```

Configure rclone to read from the MinIO bucket and decrypt:

```bash
rclone config create local-minio s3 \
  provider Minio \
  access_key_id minioadmin \
  secret_access_key minioadmin \
  endpoint http://localhost:9000 \
  region us-east-1

rclone config create local-crypt crypt \
  remote local-minio:tusd \
  filename_encryption off \
  suffix none \
  password "$(rclone obscure secret)" \
  password2 "$(rclone obscure pepper)"
```

Use the upload ID from the `Location:` output to fetch the decrypted file:

```bash
rclone cat local-crypt:<upload-id> > decrypted.txt
```

Stop the local stack when done:

```bash
docker compose -f docker-compose.tus-minio.yml down -v
```
