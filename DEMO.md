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

### Verify ABC CLI is installed

```bash
abc --version
abc config --help
abc context --help
abc secrets --help
abc data --help
```

**What you should see:** Help text for each command group.

---

## Exercise 1: Context & Configuration (5 minutes)

The current CLI uses context/config commands (not `abc auth`) to define endpoint, token, cluster, org, and workspace.

### 1.1 Create a context

```bash
abc context add dev \
  --endpoint https://dev.abc-cluster.cloud \
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
abc config list
```

**What you should see:**
- `dev` listed as active context
- Endpoint/cluster/workspace/region values
- Config keys in the local config store


---

## Exercise 1.5: Soft Onboarding (Nomad Dev Mode)

You can experience infrastructure commands without joining a real ABC cluster.

### 1.5.1 Start local Nomad in dev mode

```bash
abc --sudo infra compute add --local --dev-mode
```

**What you should see:**
- Nomad installation/config/service setup output
- Final message indicating dev mode (soft onboarding)

### 1.5.2 Inspect nodes in table format

```bash
abc --sudo infra compute list
```

### 1.5.3 Inspect nodes in JSON format

```bash
abc --sudo infra compute list --output json
```

### 1.5.4 Extract a single field via JSON path

```bash
# First node name
abc --sudo infra compute list --output json --json-path 0.Name

# Node eligibility (replace <node-id>)
abc --sudo infra compute show <node-id> --output json --json-path node.SchedulingEligibility
```

JSON path currently supports dot notation and array indexes (`foo.bar.0.baz` or `foo[0].bar`).

### 1.5.5 Reuse the saved Nomad context

After `abc infra compute add` stores the node-specific Nomad address and token in the active config,
the job/pipeline/admin Nomad interfaces reuse them automatically:

```bash
abc --sudo job list
abc pipeline run nextflow-io/hello --dry-run
abc admin services nomad cli status
```

**What you should see:**
- `job list` and `pipeline run` use the saved Nomad context when explicit flags are omitted
- `admin services nomad cli` behaves like a preconfigured `nomad` command using the same context



---

## Exercise 2: Encrypted Secrets Storage (5 minutes)

The `abc secrets` command lets you safely store sensitive credentials (tokens, API keys) encrypted at rest. This is useful for storing multiple credentials locally or for different services.

### 2.1 Set your encryption password

First, you need a password for encrypting secrets. This is the same password used for data encryption:

```bash
export ABC_CRYPT_PASSWORD="my-secret-password-123"
```

**Security Note:** In production, you would:
- Use a strong, random password (e.g., `openssl rand -base64 32`)
- Store it securely (e.g., in a password manager)
- **Never commit it to version control**

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

```bash
abc data encrypt sample_data.txt
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

```bash
abc data decrypt sample_data.txt.encrypted
```

**What you should see:**
- New file: `sample_data.txt.decrypted`
- Contains your original content

### 3.5 Verify decryption matches original

```bash
diff sample_data.txt sample_data.txt.decrypted
```

**What you should see:**
- No output (files are identical)
- Encryption → Decryption → Original matches exactly

### 3.6 Clean up

```bash
rm sample_data.txt sample_data.txt.encrypted sample_data.txt.decrypted
```

---

## Exercise 4: Understanding Data Upload (10 minutes)

Data upload uses the TUS protocol for resumable transfers. This exercise shows you the workflow without requiring a running platform.

### 4.1 Review upload capabilities

```bash
abc data upload --help
```

**What you should see:**
- **Required flags:**
  - `--endpoint`: TUS server endpoint (e.g., `https://api.example.com/files/`)
  - `--metadata key=value`: Add metadata to track uploaded files
- **Optional flags:**
  - `--catalog-api-key`: For live platform integration
  - `--dataset-name`: Tag uploads to a dataset
  - `--sample-type`: Classify file type

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

When uploading, always include metadata for tracking:

```bash
# Example (requires platform endpoint):
# abc data upload sample.fastq.gz \
#   --endpoint https://api.example.com/files/ \
#   --metadata researcher=alice \
#   --metadata project=malaria-study \
#   --metadata date=$(date -u +%Y-%m-%d)
```

**Metadata helps with:**
- Finding files later
- Tracking who uploaded what
- Recording upload date/time
- Linking to datasets or projects

---

## Exercise 5: Data Download Workflows (10 minutes)

Downloads support multiple tools (aria2, rclone, wget, s5cmd) for flexibility based on your environment.

### 5.1 Review download tools

```bash
abc data download --help
```

**What you should see:**
- **Tool options:**
  - `--tool aria2`: Fast parallel downloads (default)
  - `--tool rclone`: Cloud-friendly (S3, GCS, etc.)
  - `--tool wget`: Simple, always available
  - `--tool s5cmd`: S3-optimized
- **Source flags:**
  - `--source <uri>`: File to download (s3://, gs://, https://, local path, etc.)
  - `--source-type auto|s3|gs|http|local`: Help tool identify source
- **Destination flags:**
  - `--destination <path>`: Where to save locally
  - `--parallel N`: Maximum parallel connections

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

---

## Exercise 6: Complete Workflow Example (5 minutes)

Putting it all together: encrypt → store credential → simulate upload flow.

### 6.1 Create a realistic file

```bash
cat > genomics_data.fasta << 'EOF'
>sequence_001
ATCGATCGATCGATCGATCGATCGATCG
>sequence_002
GCTAGCTAGCTAGCTAGCTAGCTAGCTA
EOF
```

### 6.2 Encrypt it

```bash
abc data encrypt genomics_data.fasta
```

### 6.3 Store upload credentials

```bash
abc secrets set upload-token "token-from-platform" --unsafe-local
```

### 6.4 Review what you would upload

In a real scenario, you would:

```bash
# (Requires running platform - shown for reference)
# UPLOAD_TOKEN=$(abc secrets get upload-token --unsafe-local)
# abc data upload genomics_data.fasta.encrypted \
#   --endpoint https://api.example.com/files/ \
#   --metadata researcher=alice \
#   --metadata analysis=genomics_study
```

### 6.5 Clean up

```bash
rm genomics_data.fasta genomics_data.fasta.encrypted
abc secrets delete upload-token --unsafe-local
```

---

## Key Takeaways

After completing these exercises, you understand:

1. **Context setup:** Use `abc context add/use` to set endpoint, token, cluster, org, and workspace
2. **Secrets:** Use `abc secrets --unsafe-local` for encrypted credential storage
3. **Encryption:** Local encryption with `abc data encrypt` before upload
4. **Upload:** TUS protocol enables resumable transfers with metadata tracking
5. **Download:** Multiple tools available depending on your source type

---

## Next Steps

With these fundamentals in place:

- **Test with real data:** Try these workflows with your actual lab datasets
- **Explore job submission:** Use `abc job run` to execute analysis
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
- This is expected - you need a running ABC platform
- When ready, configure via `abc context add ...` and `abc context use ...`

---

## Quick Reference

| Task | Command | Notes |
|------|---------|-------|
| Add context | `abc context add <name> ...` | Endpoint/token/cluster/workspace setup |
| Use context | `abc context use <name>` | Switch active context |
| Show context | `abc context show` | Inspect active context |
| Show config | `abc config list` | View all settings |
| Set secret | `abc secrets set KEY VALUE --unsafe-local` | Encrypted storage |
| Get secret | `abc secrets get KEY --unsafe-local` | Decrypt and display |
| List secrets | `abc secrets list` | Keys only; add --unsafe-local for values |
| Encrypt file | `abc data encrypt FILE` | Creates FILE.encrypted |
| Decrypt file | `abc data decrypt FILE.encrypted` | Creates FILE.decrypted |
| Upload | `abc data upload FILE --endpoint ... --metadata ...` | Requires platform |
| Download | `abc data download --source ... --destination ... --tool aria2` | Choose tool for source type |
| Get help | `abc --help` or `abc COMMAND --help` | Detailed command info |
