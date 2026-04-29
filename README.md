# abc-cluster-cli

`abc` is the command line interface for the [abc-cluster](https://abc-cluster.io) platform, inspired by the smooth experience offered by the [tower-cli](https://github.com/seqeralabs/tower-cli).

It brings abc-cluster concepts like pipelines and ad-hoc Nomad jobs to the terminal, enabling you to launch and manage computations and automations directly from your shell.

## Installation

Install from GitHub releases using the installer script:

```bash
# Download the correct binary for your OS/arch into the current directory.
# Uses the GitHub Contents API (not raw.githubusercontent.com) so `ref=main`
# is not stuck on an outdated CDN snapshot of the installer script.
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh

# Install to /usr/local/bin/abc (prompts for sudo password)
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh -s -- --sudo
```

Install a specific release:

```bash
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh -s -- --version v1.2.3
```

Alternative install methods:

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

### Config file (`~/.abc/config.yaml`)

Persistent settings live in `~/.abc/config.yaml` (override path with `ABC_CONFIG_FILE`). Use `abc context add` / `abc config set` to manage them. See **[USAGE.md](./USAGE.md)** for the full key list.

Important distinctions:

- **`contexts.<name>.endpoint`** — ABC control-plane API base URL (often ends with `/v1`).
- **`contexts.<name>.region`** — ABC / workspace **label** (e.g. `za-cpt`), not Nomad’s RPC region.
- **`contexts.<name>.admin.services.nomad`** — Defaults for Nomad: **`nomad_addr`** (persist `http://HOST:PORT` with an explicit port, like other `admin.services.*` URLs; **`nomad_token`**; optional **`nomad_region`** (Nomad multi-region id, e.g. `global`). CLI flags and `NOMAD_ADDR` may still use bare `http://host` and get `:4646` by default at runtime.)

Additional optional environment variables:

| Variable            | Description                                       | Default                        |
|---------------------|---------------------------------------------------|--------------------------------|
| `ABC_API_ENDPOINT`  | abc-cluster API URL                               | `https://api.abc-cluster.io`   |
| `ABC_WORKSPACE_ID`  | Workspace ID to use for operations                | *(user's default workspace)*   |
| `ABC_UPLOAD_ENDPOINT` | Tus upload endpoint used by `abc data upload` (falls back to context upload endpoint or `<url>/files/` from the API URL) | *(unset)* |
| `ABC_UPLOAD_TOKEN`  | Bearer token used by `abc data upload` for tus auth (falls back to context upload token or `ABC_ACCESS_TOKEN`) | *(unset)* |
| `ABC_CLI_DISABLE_UPDATE_CHECK` | Disable automatic GitHub release update notifications (`1`, `true`, `yes`, `on`) | *(unset)* |
| `NOMAD_ADDR` / `NOMAD_TOKEN` / `NOMAD_REGION` | Override Nomad connection for one shell session | *(unset)* |

## Usage

```
abc <command> <subcommand> [flags]
```

| Command group    | Subcommands                                    | Description                                  |
|------------------|------------------------------------------------|----------------------------------------------|
| `pipeline`       | `run`, `add`, `list`, `info`, `update`, `delete`, `export`, `import`, `params` | Submit and manage Nextflow pipeline runs |
| `job`            | `run`, `list`, `show`, `stop`, `logs`, `status`, `dispatch` | Submit and manage Nomad batch jobs |
| `module`         | `run`, `samplesheet emit` | Generate and run nf-core module driver pipelines on Nomad (via nf-pipeline-gen) |
| `data`           | `upload`, `encrypt`, `decrypt`, `download`     | Upload and manage data files                 |
| `secrets`        | `set`, `get`, `list`, `delete`, `ref`, `backend setup` | Manage secrets (local / Nomad Variables / Vault KV v2) |
| `cluster`        | `capabilities sync`, `capabilities show`, `list`, `status`, `provision`, `decommission` | Inspect and manage clusters |

For detailed flag references, examples, and preamble directive documentation, see **[USAGE.md](./USAGE.md)**.

### Quick examples

```bash
# Run a pipeline
abc pipeline run https://github.com/org/my-pipeline --revision main

# Generate a Nomad HCL job spec from an annotated script
abc job run myjob.sh

# Submit a job directly to Nomad and stream logs
abc job run myjob.sh --submit --watch

# Upload a file (tus endpoint / token from context, ABC_UPLOAD_*, or flags)
# On abc-nodes contexts: auto-discovers tusd URL after 'abc cluster capabilities sync'
abc data upload ./data.csv

# Encrypt a file before uploading
abc data encrypt ./data.csv --crypt-password "secret"
abc data upload ./data.csv.encrypted

# Cluster-side download job (Nomad): --destination is path inside the task; --node pins placement
abc data download --tool wget --driver containerd --source https://example.com/file.zip --destination /tmp/dl --node my-nomad-node

# nf-core module: scaffold a starter samplesheet from the module's bundled tests
abc module samplesheet emit nf-core/plink/extract
# → ./samplesheet-nf-core-plink-extract.csv (downloaded from a one-shot Nomad job)

# Run the module driver against an edited samplesheet — validated cluster-side before driver gen
abc module run nf-core/plink/extract --samplesheet ./samples.csv
```

### abc-nodes secrets & capabilities

```bash
# Discover running services on an abc-nodes cluster and populate config
abc cluster capabilities sync
abc cluster capabilities show

# Store a secret in Nomad Variables (abc-nodes)
abc secrets set my-key "s3cr3t" --backend nomad

# Store a secret in Vault KV v2 (abc-nodes with Vault)
export VAULT_TOKEN=<root-token>
abc secrets set my-key "s3cr3t" --backend vault

# Get the Nomad template ref for a secret (use in pipeline params, job scripts)
abc secrets ref my-key --backend nomad   # → {{ with nomadVar "abc/secrets/default/my-key" }}...{{ end }}

# Use a secret as a pipeline parameter — translated to a template ref at submit time
abc pipeline run rnaseq --params-file params.yaml
# params.yaml: { input: "secret://s3-key" }  → nomadVar ref injected into params.json at runtime

# Local crypt defaults (no backend required)
abc secrets init --unsafe-local
abc secrets set aws-key "AKIA..." --unsafe-local

# Run open-source service CLIs through the unified abc wrapper
abc admin services cli nebula -- -version
abc admin services cli rustfs -- ls
abc admin services cli vault -- status
abc admin services cli traefik -- version
abc admin services cli pulumi -- stack ls
abc admin services cli terraform -- plan

# Per-service form (equivalent, still fully supported)
abc admin services pulumi cli -- stack ls
abc admin services vault cli status
```

### Nomad floor jobs (`abc-nodes`)

Example **Nomad** service specs for MinIO, RustFS, tusd, Prometheus, Grafana, Loki, ntfy, Vault, and Traefik live under **`deployments/abc-nodes/nomad/`**. Validate or run them with the Nomad CLI passthrough (uses your active abc context for `NOMAD_ADDR` / token):

```bash
abc admin services cli nomad -- job validate deployments/abc-nodes/nomad/minio.nomad.hcl
abc admin services cli nomad -- job run -detach deployments/abc-nodes/nomad/minio.nomad.hcl
```

Deploy Pulumi-managed userspace resources (working directory and stack resolved from `admin.services.pulumi` in the active context):

```bash
abc admin services cli pulumi -- stack ls
abc admin services cli pulumi -- up --yes
abc admin services cli pulumi -- destroy --yes
```

After services are running, sync discovered endpoints and capabilities to your config:

```bash
abc cluster capabilities sync    # auto-populates admin.services.* URLs and capabilities block
abc cluster capabilities show    # view what was detected
```

HashiCorp Vault (opt-in) lives under **`deployments/abc-nodes/experimental/nomad/vault.nomad.hcl`** with **Raft integrated storage** (data at `/opt/nomad/vault/data`). See **`deployments/abc-nodes/experimental/README.md`**.

See **`deployments/abc-nodes/nomad/README.md`** for host volumes, variable overrides, and ordering. Curated **`nomad-pack`** bundles for **base** (MinIO + tusd) vs **enhanced** (+ monitoring stack) are under **`deployments/abc-nodes/nomad-packs/`** (see the same README). Operator guide for Prometheus/Grafana/validation scripts: **`docs/abc-nodes-observability-and-operations.md`**.

Deploy the enhanced pack through the abc CLI passthrough so the active bootstrap context supplies the Nomad connection details:

```bash
abc admin services cli nomad-pack -- run deployments/abc-nodes/nomad-packs/abc_nodes_enhanced
```

## Development

```bash
# Run tests
go test ./...

# Or use Just (vet, mod verify, tests)
just check

# Build
go build -o abc .

# Install a release-style binary to ~/bin (from repo root when using this package’s justfile)
just install-local

# Build multi-platform binaries locally (linux/darwin/windows x amd64/arm64)
bash scripts/local-matrix-build.sh

# Diagnose Slurm control-plane connectivity from Nomad login node (--region here is Nomad RPC region)
abc job run scripts/slurm-cli-diagnose.sh --submit --watch --region global
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
