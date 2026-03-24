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

## Development

```bash
# Run tests
go test ./...

# Build
go build -o abc .
```
