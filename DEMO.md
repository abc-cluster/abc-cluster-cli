# ABC CLI Hands-On Guide

A step-by-step walkthrough of the ABC CLI focused on data management: encryption, uploads, downloads, and job monitoring. Work through these exercises at your own pace.

## Overview

This guide covers:

1. **Context & Configuration** — Set up endpoint, cluster/workspace context, and token
2. **Encrypted Secrets Management** — Store API keys and credentials securely (AES-256-GCM)
3. **Local Data Encryption/Decryption** — Prepare sensitive data before upload
4. **Resumable Data Upload** — Upload files with checkpoint recovery (TUS protocol)
5. **Data Download Workflows** — Download from remote sources or pipeline outputs
6. **Complete Workflows** — End-to-end examples combining all features

**Time:** ~30 minutes total
**Difficulty:** Beginner-friendly, assumes CLI familiarity

## Prerequisites

- ABC CLI installed: `abc --version`
- A terminal/command line
- (Optional) Access to the ABC platform API endpoint and token
- Sample data (will be created during exercises)

---

## Setup Phase (5 minutes)

### Create a demo workspace

```bash
mkdir -p ~/abc-demo/sample-data ~/abc-demo/encrypted-data
cd ~/abc-demo
```

This is your working directory for all exercises.

### Generate local crypt defaults (recommended)

Stores `defaults.crypt_password` / `defaults.crypt_salt` in `~/.abc/config.yaml` so `abc secrets` and `abc data encrypt` can run without exporting `ABC_CRYPT_PASSWORD` each time:

```bash
abc secrets init --unsafe-local
```

### Verify ABC CLI is installed

```bash
abc --version
```

**What you should see:** Help text for the CLI.

---

## Exercise 1: Configuration and Context (5 minutes)

The current CLI uses context/config commands (not `abc auth`) to define endpoint, token, cluster, org, and workspace.

Create a config file and add a context to it.
```bash
abc config init
```

**What you should see:**
- Config file created at `~/.abc/config.yaml`
- Context added to the config file

### 1.1 Create a context

```bash
abc context add dev \
  --endpoint https://dev.abc-cluster.cloud \
  --upload-token "UPLOAD_TOKEN" \
  --access-token "TOKEN" \
  --cluster dev-cluster \
  --organization-id org-dev \
  --workspace-id ws-dev \
  --region za-cpt
```

### 1.2 Activate and inspect the context

```bash
abc context use dev
abc context show
abc context list

```

**What you should see:**
- `dev` listed as active context
- Endpoint/cluster/workspace/region values
- Config keys in the local config store

---

## Exercise 2: Encrypted Secrets Storage (5 minutes)

The `abc secrets` command lets you safely store sensitive credentials (tokens, API keys) encrypted at rest. This is useful for storing multiple credentials locally or for different services.

### 2.1 Encryption password

If you already ran `abc secrets init --unsafe-local` in the setup phase, the CLI uses the generated `crypt_password` / `crypt_salt` from config for `abc secrets` and `abc data encrypt`.

Otherwise, export a passphrase for this session (same variable used for data encryption):

```bash
export ABC_CRYPT_PASSWORD="my-secret-password-123"
```

**Security Note:** In production, prefer `abc secrets init --unsafe-local` or a strong random passphrase (`openssl rand -base64 32`), store it in a password manager, and **never commit it to version control**.

### 2.2 Store an encrypted secret

Let's store a test API key:

```bash
abc secrets set demo-api-key "sk-1234567890abcdef" --unsafe-local
```

**What you should see:**
- No output (silent success)
- Secret is encrypted with AES-256-GCM
- Stored in `~/.abc/config.yaml` under a `secrets:` section
- If the config already contains `crypt_password`/`crypt_salt`, those values are canonical and will be used instead of any env vars

### 2.3 Verify it's encrypted

Look at your config file:

```bash
cat ~/.abc/config.yaml
```

**What you should see:**
- Your secret listed under `secrets:`
- The value is encrypted (looks like: `nonce...|ciphertext...` in base64)
- Your plaintext value **is not visible**

### 2.4 Retrieve the decrypted secret

```bash
abc secrets get demo-api-key --unsafe-local
```

**What you should see:**
- Your original plaintext value: `sk-1234567890abcdef`
- If `~/.abc/config.yaml` already contains `crypt_password`/`crypt_salt`, those are used preferentially
- If env vars differ from config, the CLI warns and uses the config values

### 2.5 Manage contexts

You can view and switch between saved contexts using `abc context`.

```bash
abc context list
abc context use dev
abc context show
```

Example dev/staging setup:
```bash
abc context add dev \
  --endpoint https://dev.abc-cluster.cloud \
  --access-token "TOKEN" \
  --cluster dev-cluster \
  --organization-id org-dev \
  --workspace-id ws-dev \
  --region za-cpt

abc context add staging \
  --endpoint https://staging.abc-cluster.cloud \
  --access-token "TOKEN" \
  --cluster staging-cluster \
  --organization-id org-staging \
  --workspace-id ws-staging \
  --region eu-cpt
```

### 2.6 List all secrets

```bash
abc secrets list
```

**What you should see:**
- List of secret keys (without values)
- Example: `demo-api-key`

To see all values decrypted:

```bash
abc secrets list --unsafe-local
```

**What you should see:**
- Key-value pairs (keys and decrypted values)

---

## Exercise 3: Local Data Encryption & Decryption (10 minutes)

Before uploading data, you can encrypt it locally using the same password. This ensures data is encrypted at rest before transfer.

### 3.1 Create a test file

Create some sample data to encrypt:

```bash
cat > sample_data.txt << 'EOF'
Sample bioinformatics data
sequence: ATCGATCGATCG
metadata: experiment_001
EOF
```

**What you should see:**
- File `sample_data.txt` created in current directory

### 3.2 Encrypt the file

With `defaults.crypt_password` from `abc secrets init --unsafe-local`, or after a first encrypt with `--crypt-password`, you can encrypt without flags:

```bash
abc data encrypt sample_data.txt
```

Or pass a password explicitly (stored in config for later commands):

```bash
abc data encrypt sample_data.txt --crypt-password "demo-secret"
```

**What you should see:**
- New file: `sample_data.txt.encrypted` (contains encrypted binary data)
- Original file is still present (not deleted)
- Size of encrypted file is similar to original (plus overhead for IV/salt)

### 3.3 Verify encryption

Check the encrypted file is truly encrypted:

```bash
file sample_data.txt.encrypted
cat sample_data.txt.encrypted | head -c 50
```

**What you should see:**
- File type: `data` (not UTF-8 text)
- Content is binary/unreadable (not your original text)

### 3.4 Decrypt the file

Because the plaintext `sample_data.txt` still exists, decryption writes to a sibling path to avoid overwriting:

```bash
abc data decrypt sample_data.txt.encrypted --output sample_data.roundtrip.txt
```

**What you should see:**
- New file: `sample_data.roundtrip.txt` with your original content

### 3.5 Verify decryption matches original

```bash
diff sample_data.txt sample_data.roundtrip.txt
```

**What you should see:**
- No output (files are identical)
- Encryption → Decryption → Original matches exactly

### 3.6 Clean up

```bash
rm -f sample_data.txt sample_data.txt.encrypted sample_data.roundtrip.txt
```

---

## Exercise 4: Understanding Data Upload (10 minutes)

Data upload uses the TUS protocol for resumable transfers. Endpoint and bearer token are resolved at run time in this order: non-empty `--endpoint` / `--upload-token` if you passed them, then `ABC_UPLOAD_ENDPOINT` / `ABC_UPLOAD_TOKEN`, then the active context’s `upload_endpoint` / `upload_token`, then `<API url>/files/` (derived from the context or `--url` API endpoint, without duplicate slashes), then `--access-token`.

### 4.1 Review upload capabilities

```bash
abc data upload --help
```

**What you should see (high level):**
- `--endpoint` — tus root URL (optional when context or env configures it)
- `--upload-token` — bearer for tus (optional; falls back to context or `--access-token`)
- `--meta key=value` — extra tus metadata (repeatable)
- `--status` / `--clear` — inspect or clear local resume state
- Client-side encryption flags: `--crypt-password`, `--crypt-salt`

### 4.2 Understand TUS resumable transfers

The TUS (Tus Resumable Upload Protocol) is designed for:
- **Large files:** Multi-part uploads
- **Unreliable networks:** Pause and resume
- **Progress tracking:** Real-time upload status

**How it works:**
```
1. Create upload session (POST to /files/)
2. Chunk data into blocks (e.g., 5MB each)
3. Upload chunks sequentially
4. If interrupted, resume from last successful chunk
5. Mark complete when all chunks uploaded
```

### 4.3 Metadata best practices

When uploading, include metadata for tracking:

```bash
# With upload_endpoint + upload_token on the active context (see context add):
# abc data upload ./sample.fastq.gz \
#   --meta researcher=alice \
#   --meta project=malaria-study \
#   --meta date=$(date -u +%Y-%m-%d)
```

**Metadata helps with:**
- Finding files later
- Tracking who uploaded what
- Recording upload date/time
- Linking to datasets or projects

---

## Exercise 5: Data Download Workflows (10 minutes)

For tools other than `nextflow`, `abc data download` builds a small shell script and runs `abc job run --submit` so the transfer runs on your Nomad cluster (not necessarily on your laptop).

### 5.1 Review download flags

```bash
abc data download --help
```

**What you should see:**
- **`--tool`:** `aria2` (default), `rclone`, `wget`, `s5cmd`, or `nextflow` (fetchngs pipeline mode)
- **`--source` / `--url-file`:** What to download
- **`--destination`:** Path **inside the task** where files are written (e.g. `/tmp/my-dl`), or a special target such as `abc-bucket`
- **`--node`:** Nomad **node** placement — full node UUID or node name; adds `#ABC --constraint=node.unique.id==...` or `node.unique.name==...` to the generated script
- **`--driver`:** `exec` (host binaries) or `docker` (pinned images; recommended with `--node` unless the node already has your tool)
- **`--parallel`**, **`--tool-args`**, **`--name`**

### 5.2 Understand download tool selection

**Choose your tool based on source:**

| Source | Recommended | Why |
|--------|-------------|-----|
| S3 bucket | `s5cmd` or `rclone` | Cloud-optimized, handles credentials |
| Google Cloud Storage | `rclone` | Better GCS integration |
| HTTP/HTTPS URL | `aria2` or `wget` | Simple HTTP protocol |
| Local NFS mount | `wget` or direct cp | No remote tool needed |

### 5.3 Credentials for cloud sources

For S3 or GCS downloads, ensure credentials are configured:

```bash
# AWS S3 (use your actual credentials)
export AWS_ACCESS_KEY_ID="your-access-key"
export AWS_SECRET_ACCESS_KEY="your-secret-key"

# Google Cloud Storage
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/credentials.json"
```

### 5.4 Example: cluster download job (optional)

Pick a small public URL for a smoke test. This submits a Nomad job, so your machine must reach the Nomad API (normally after an operator runs `abc infra compute add`, or by exporting `NOMAD_ADDR` and `NOMAD_TOKEN` for this shell). Use `--region global` on submit only when your cluster expects that Nomad RPC region (see **USAGE.md** if unsure).

```bash
# Pin to a node you can access (UUID from `nomad node status` or node name).
# Prefer --driver docker so the image provides wget; with --driver=exec the node must have wget.
abc data download \
  --tool wget \
  --driver docker \
  --source https://speed.hetzner.de/100MB.bin \
  --destination /tmp/abc-demo-dl \
  --node YOUR_NOMAD_NODE_NAME_OR_UUID \
  --name demo-dl-smoke
```

After the job completes, use the downloaded bytes on that node as input to other steps (re-upload, checksum, encrypt) by wrapping further commands in your own `abc job run` script or extending the download script pattern.

### 5.5 Local temp file for other commands

When you only need a **local** large object to practice `abc data upload` (no cluster), create a sparse 1 GiB file so disk usage stays small:

```bash
# macOS — sparse 1 GiB file
mkfile -n 1g ./large-sample.bin

# Linux — dense 1 GiB (takes real disk); use fallocate -l 1G where available for sparse
if command -v mkfile >/dev/null 2>&1; then mkfile -n 1g ./large-sample.bin
elif command -v truncate >/dev/null 2>&1; then truncate -s 1G ./large-sample.bin
else dd if=/dev/zero of=./large-sample.bin bs=1M count=1024
fi
```

Use `./large-sample.bin` with `abc data upload` in the next exercise.

---

## Exercise 6: Complete Workflow Example (10 minutes)

Large upload smoke test: local sparse file → optional encrypt → upload using context tus settings.

### 6.1 Create a ~1 GiB test file

```bash
# macOS (sparse)
mkfile -n 1g ./large-sample.bin

# Linux: see §5.5 for truncate/dd fallback
```

### 6.2 (Optional) Encrypt before upload

Encrypting 1 GiB can take noticeable CPU time; skip this step if you only want to exercise tus:

```bash
abc data encrypt ./large-sample.bin --output ./large-sample.bin.encrypted
```

### 6.3 Ensure tus settings are on the context

Prefer `upload_endpoint` / `upload_token` on the context (see Exercise 1.1) or set `ABC_UPLOAD_ENDPOINT` / `ABC_UPLOAD_TOKEN`. You can still store a long-lived tus token as a secret:

```bash
abc secrets set upload-token "token-from-platform" --unsafe-local
```

### 6.4 Upload (uses context / env resolution)

```bash
# When upload_endpoint + upload_token are configured on the active context:
# abc data upload ./large-sample.bin \
#   --meta researcher=alice \
#   --meta analysis=demo-large-upload

# If you kept the tus token only in secrets, pass it explicitly:
# abc data upload ./large-sample.bin \
#   --upload-token "$(abc secrets get upload-token --unsafe-local)" \
#   --meta analysis=demo-large-upload
```

### 6.5 Clean up

```bash
rm -f ./large-sample.bin ./large-sample.bin.encrypted
```

---

## Exercise 7: `abc job run` (ABC and SLURM) (10 minutes)

Use `abc job run` to submit shell scripts with `#ABC` directives.
This exercise shows both pure ABC execution and SLURM-enabled execution.

**Nomad connectivity:** Commands that use `--submit` talk to Nomad from your laptop. In real clusters an operator usually runs `abc infra compute add` once so your active context picks up connection defaults; for ad-hoc use you can `export NOMAD_ADDR=...` and `export NOMAD_TOKEN=...` (include `:4646` in the address unless your operator documents otherwise).

### 7.1 Pure ABC job (no SLURM)

Create a ABC-native smoke script:

```bash
cat > ABC-smoke.sh << 'EOF'
#!/bin/bash
#ABC --name=ABC-smoke-demo
#ABC --driver=raw_exec
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=128M
set -euo pipefail
echo "ABC smoke test"
echo "HOSTNAME=$(hostname)"
echo "DATE=$(date -Is)"
EOF
chmod +x ABC-smoke.sh
```

Inspect generated HCL first:

```bash
abc job run ABC-smoke.sh
```

Submit and stream logs:

```bash
abc job run ABC-smoke.sh --submit --watch --region global
```

### 7.2 SLURM-enabled job (hybrid preamble)

Create a script that includes both `#SBATCH` and `#ABC` directives:

```bash
cat > hybrid-slurm-smoke.sh << 'EOF'
#!/bin/bash
#SBATCH --job-name=hybrid-slurm-smoke-demo
#SBATCH --cpus-per-task=1
#SBATCH --mem=256M
#SBATCH --time=00:02:00
#ABC --name=hybrid-slurm-smoke-demo
#ABC --driver=slurm
#ABC --dc=gcp-slurm
set -euo pipefail
echo "Hybrid preamble smoke test"
echo "HOSTNAME=$(hostname)"
echo "SLURM_JOB_ID=${SLURM_JOB_ID:-unset}"
EOF
chmod +x hybrid-slurm-smoke.sh
```

Inspect translation to ABC HCL:

```bash
abc job run hybrid-slurm-smoke.sh
```

Submit and stream logs:

```bash
abc job run hybrid-slurm-smoke.sh --submit --watch --region global
```

### 7.3 Verify status and cleanup

```bash
abc job list --status running
# Optionally stop a job:
# abc job stop <job-id> --yes
rm -f ABC-smoke.sh hybrid-slurm-smoke.sh
```

Notes:
- ABC example requires a reachable ABC cluster and valid ACL token/context.
- SLURM example also requires the `slurm` task driver to be available on eligible ABC clients.

---

## Key Takeaways

After completing these exercises, you understand:

1. **Context setup:** Use `abc context add/use` to set API endpoint, optional tus `upload_endpoint` / `upload_token`, access token, cluster, org, and workspace
2. **Secrets:** `abc secrets init --unsafe-local` for local crypt material; `abc secrets --unsafe-local` for encrypted credential storage
3. **Encryption:** Local encryption with `abc data encrypt` before upload
4. **Upload:** TUS resumable uploads; endpoint/token resolved from flags, env, or context
5. **Download:** Tool-based downloads run as Nomad jobs; `--destination` is the task path; `--node` pins placement
6. **Job execution:** ABC-native and SLURM-enabled jobs via `abc job run` (with Nomad reachable via `abc infra compute add` or `NOMAD_ADDR` / `NOMAD_TOKEN` in the environment)

---

## Next Steps

With these fundamentals in place:

- **Test with real data:** Try these workflows with your actual lab datasets
- **Explore job submission:** Use `abc job run` (ABC + SLURM paths) to execute analysis
- **Set up monitoring:** Use `abc job logs` and `abc job status` to track runs
- **Read the docs:** See `docs/abc-cli-design-v7.md` for complete reference
- **Get help:** Run `abc --help` or `abc <command> --help` for more options

---

## Troubleshooting

### "ABC_CRYPT_PASSWORD not set"
```bash
export ABC_CRYPT_PASSWORD="your-password"
```

### "Cannot decrypt secret"
- Verify ABC_CRYPT_PASSWORD matches what you used to set it
- Check secret exists: `abc secrets list`

### "Decrypted file doesn't match original"
- Same password must be used for encrypt and decrypt
- Check file wasn't corrupted during transfer
- Try encrypting a fresh copy

### "Upload fails without endpoint"
- Set `upload_endpoint` on the context (or rely on default `<endpoint>/files/` from `abc context add`), or export `ABC_UPLOAD_ENDPOINT`, or pass `--endpoint` to `abc data upload`
- Ensure the tus root returns `Tus-Version` on `OPTIONS`

### "No path to region" or other Nomad errors on `abc job run` / `abc data download`
- For `--submit`, ensure `NOMAD_ADDR` includes the Nomad HTTP port (usually **`:4646`**).
- The `--region` flag on `abc job run` is Nomad’s **RPC** region (often `global`), not the same value as `--region` on `abc context add` (that one is an ABC workspace label).
- Ask your operator to run `abc infra compute add` so your context gets Nomad defaults, or keep using `NOMAD_ADDR` / `NOMAD_TOKEN` exports for the session.

---

## Quick Reference

| Task | Command | Notes |
|------|---------|-------|
| Add context | `abc context add <name> ...` | Endpoint/token/cluster/workspace setup |
| Use context | `abc context use <name>` | Switch active context |
| Show context | `abc context show` | Inspect active context |
| Show config | `abc config list` | View all settings |
| Init crypt defaults | `abc secrets init --unsafe-local` | Writes `defaults.crypt_password` / `crypt_salt` |
| Set secret | `abc secrets set KEY VALUE --unsafe-local` | Encrypted storage |
| Get secret | `abc secrets get KEY --unsafe-local` | Decrypt and display |
| List secrets | `abc secrets list` | Keys only; add --unsafe-local for values |
| Encrypt file | `abc data encrypt FILE` | Creates `FILE.encrypted` by default |
| Decrypt file | `abc data decrypt FILE.encrypted` | Strips `.encrypted` or use `--output` |
| Upload | `abc data upload FILE [--meta k=v ...]` | Tus; endpoint/token from context/env/flags |
| Download (tools) | `abc data download --tool wget --source URL --destination /tmp/x [--node NODE]` | Submits `abc job run --submit` |
| Download (fetchngs) | `abc data download --tool nextflow --accession SRR...` | Pipeline mode |
| Job run (ABC) | `abc job run SCRIPT --submit --watch --region global` | Use `#ABC --driver=raw_exec` |
| Job run (SLURM) | `abc job run SCRIPT --submit --watch --region global` | Use `#ABC --driver=slurm` + `#SBATCH` |
| Get help | `abc --help` or `abc COMMAND --help` | Detailed command info |
