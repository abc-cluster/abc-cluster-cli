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

```
abc <command> <subcommand> [flags]
```

| Command group    | Subcommands                                    | Description                                  |
|------------------|------------------------------------------------|----------------------------------------------|
| `pipeline`       | `run`                                          | Submit and manage Nextflow pipeline runs      |
| `job`            | `run`, `list`, `show`, `stop`, `logs`, `status`, `dispatch` | Submit and manage Nomad batch jobs |
| `data`           | `upload`, `encrypt`, `decrypt`, `download`     | Upload and manage data files                 |

For detailed flag references, examples, and preamble directive documentation, see **[USAGE.md](./USAGE.md)**.

### Quick examples

```bash
# Run a pipeline
abc pipeline run --pipeline https://github.com/org/my-pipeline --revision main

# Generate a Nomad HCL job spec from an annotated script
abc job run myjob.sh

# Submit a job directly to Nomad and stream logs
abc job run myjob.sh --submit --watch

# Upload a file
abc data upload ./data.csv

# Encrypt a file before uploading
abc data encrypt ./data.csv --crypt-password "secret"
abc data upload ./data.csv.bin
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

### GCP Slurm pure-SLURM e2e validation

Use the dedicated validation suite for pure `#SBATCH` submission through `abc job run` to a Slurm-enabled Nomad client:

```bash
NOMAD_ADDR=http://<nomad>:4646 \
NOMAD_TOKEN=<nomad-acl-token> \
./validation/gcp_slurm/validate-pure-slurm-gcp.sh
```

Fixtures used by this suite:

- `validation/gcp_slurm/pure-slurm-hello.sbatch.sh`
- `validation/gcp_slurm/pure-slurm-array.sbatch.sh`

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
