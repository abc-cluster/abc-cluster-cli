# ABC CLI Hands-On Guide

A modular walkthrough of the **ABC CLI**: each **Exercise** is written so a motivated user can finish it in **about ten minutes** (skip optional sub-steps if you need to stay on budget). Work through any subset in order, or treat the guide as a menu.

## Overview

The ABC CLI is the day-to-day interface for **cluster-backed workflows**: configure where you are (`context` / `config` / `auth`), protect credentials (`secrets`), move and protect data (`data encrypt`, `data upload`, `data download`), turn annotated shell scripts into **Nomad** jobs (`job run`), and dispatch higher-level work (`pipeline run`, `module run`, `submit`). Operators also use **`infra compute`** and **`admin services …`** to wire clusters and service control planes.

**What this guide covers (hands-on):**

| Exercise | Focus |
|----------|--------|
| [1](#exercise-1-workspace-and-context-setup) | Workspace layout, local crypt defaults, config/context (or auth login) |
| [2](#exercise-2-encrypted-secrets) | Encrypted secrets in local config |
| [3](#exercise-3-encrypt-and-decrypt-files) | File encryption and round-trip decrypt |
| [4](#exercise-4-upload-concepts) | TUS resumable upload flags and behaviour |
| [5](#exercise-5-download-workflows) | Cluster-side downloads via generated jobs |
| [6](#exercise-6-large-upload-smoke) | Large-file upload smoke (optional encrypt) |
| [7](#exercise-7-job-run-and-nomad) | `abc job run`: `#ABC` directives, **`--task-tmp`**, optional Pixi stack, SLURM hybrid |
| [8](#exercise-8-more-cli-commands) | Help-only tour: `pipeline`, `module`, `submit`, `infra` |

**Time budget:** roughly **10 minutes × number of exercises** you complete (~80 minutes for a full pass). You do **not** need a live cluster for Exercises 1–4, 8, or for **HCL-only** parts of Exercise 7 (omit **`--submit`**; that prints Nomad job HCL locally).

**Difficulty:** Beginner-friendly; assumes comfort in a shell.

**Deeper reference:** [`USAGE.md`](USAGE.md) (command flags and preamble tables), [`docs/abc-cli-design-v7.md`](docs/abc-cli-design-v7.md) (design notes).

## Prerequisites

- **`abc` on your `PATH`:** `abc --version`. If you do not have it yet, install from GitHub releases using the installer script (downloads the correct binary for your OS/arch):
  ```bash
  # Install into the current directory (see script output for PATH hints)
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash
  ```
  Install to `/usr/local/bin/abc` (prompts for your sudo password):
  ```bash
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash -s -- --sudo
  ```
  Pin a specific release (replace the tag with the version you want):
  ```bash
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash -s -- --version v1.2.3
  ```
  Other install paths (`go install`, build from source): [README.md — Installation](README.md#installation).
- A terminal
- (Optional) ABC platform API URL and tokens, and/or Nomad URL and ACL token for submit paths
- Sample data: created in the exercises below

**Tip (optional):** keep your usual `~/.abc/config.yaml` untouched by exporting **`ABC_CONFIG_FILE=/path/to/disposable.yaml`** for this walkthrough; all commands respect that path.

---

## Exercise 1: Workspace and context setup

**Time target:** about 10 minutes.

You will create a small working tree, initialize **local crypt material** (so `secrets` / `data encrypt` work without exporting a password every time), and save an **API context** (endpoint, tokens, organization / workspace / region labels). Use **`abc auth login`** for an interactive first-time setup, or **`abc context add`** if an operator gave you exact values.

### 1.1 Create a demo workspace

```bash
mkdir -p ~/abc-demo/sample-data ~/abc-demo/encrypted-data
cd ~/abc-demo
```

### 1.2 Ensure config exists (first run on this machine)

On a laptop that has **never** run the CLI before, create `~/.abc/config.yaml` and a placeholder **`default`** context:

```bash
abc config init
```

Skip this if you already have `~/.abc/config.yaml` (or `ABC_CONFIG_FILE`).

### 1.3 Add a context (example)

Replace placeholders with values from your operator. This command **creates the context and makes it active** (you will generate crypt material for **that** context in the next step).

```bash
abc context add dev \
  --endpoint https://dev.abc-cluster.cloud \
  --upload-token "UPLOAD_TOKEN" \
  --access-token "TOKEN" \
  --organization-id org-dev \
  --workspace-id ws-dev \
  --region za-cpt
```

If tus is **not** at `<endpoint>/files/`, add **`--upload-endpoint https://…`** to the same command (or add a second context under another name, e.g. `staging`, with the full flag set).

Optional: record the platform tier for this context (`abc-nodes`, `abc-cluster`, or `abc-cloud`):

```bash
abc config set contexts.dev.cluster_type abc-cluster
```

### 1.4 Local crypt defaults (recommended)

`abc secrets init --unsafe-local` stores **`crypt.password`** / **`crypt.salt`** on the **active** context (the one you use for `abc secrets` / `abc data encrypt`). Run it **after** you have created and activated the context you will actually use (here: **`dev`** from step **1.3**).

```bash
abc secrets init --unsafe-local
```

If you later `abc context use other` and that context has no crypt block yet, run **`abc secrets init --unsafe-local`** again for that context (or use **`--force`** to rotate material — see `abc secrets init --help`).

### 1.5 Verify the CLI

```bash
abc --version
```

### 1.6 Configuration entrypoints

- **`abc auth login`** — interactive login; prompts for endpoint and token and creates or updates a context (good first run on a laptop). Run **`abc secrets init --unsafe-local`** afterward for that new context if you will use local encrypted secrets.
- **`abc config init`** — creates `~/.abc/config.yaml` and a placeholder **`default`** context with the public API endpoint preset; **no** interactive token prompts (then use **`abc auth login`** or **`abc context add`**).
- **`abc context add`** — non-interactive when you already have URLs and tokens (used in step **1.3**).

### 1.7 Activate and inspect

```bash
abc context use dev
abc context show
abc context list
```

**What you should see:** `dev` as the active context; endpoint, workspace, organization, and region fields (and upload fields if you set them); entries in `~/.abc/config.yaml`.

**Optional (same exercise if you have time):** add a second context (e.g. `staging`) by repeating `abc context add` with different `--endpoint` / IDs, then `abc context use` to switch (and run **`abc secrets init --unsafe-local`** once per context that needs local crypt).

---

## Exercise 2: Encrypted secrets

**Time target:** about 10 minutes.

Store API keys and similar values **encrypted at rest** in your local config.

### 2.1 Encryption password

If you ran `abc secrets init --unsafe-local` in Exercise 1, prefer that material. Otherwise for a single session:

```bash
export ABC_CRYPT_PASSWORD="my-secret-password-123"
```

**Security:** In production use a strong random passphrase (`openssl rand -base64 32`), a password manager, and **never commit** secrets or crypt material to version control.

### 2.2 Store a secret

```bash
abc secrets set demo-api-key "sk-1234567890abcdef" --unsafe-local
```

### 2.3 Confirm it is not stored in plaintext

```bash
grep -n demo-api-key "${ABC_CONFIG_FILE:-$HOME/.abc/config.yaml}" || true
cat "${ABC_CONFIG_FILE:-$HOME/.abc/config.yaml}"
```

**What you should see:** an entry under `secrets:` whose value looks like opaque base64 (not the raw API key).

### 2.4 Read it back

```bash
abc secrets get demo-api-key --unsafe-local
```

### 2.5 List keys (and optionally values)

```bash
abc secrets list
abc secrets list --unsafe-local
```

---

## Exercise 3: Encrypt and decrypt files

**Time target:** about 10 minutes.

Encrypt files locally with the same crypt material as `secrets`.

### 3.1 Create a small plaintext file

```bash
cat > sample_data.txt << 'EOF'
Demo payload line one
Demo payload line two
checksum_seed=42
EOF
```

### 3.2 Encrypt

```bash
abc data encrypt sample_data.txt
```

Or pass a password once (then stored in config for later commands):

```bash
abc data encrypt sample_data.txt --crypt-password "demo-secret"
```

**What you should see:** `sample_data.txt.encrypted` alongside the original.

### 3.3 Inspect ciphertext

```bash
file sample_data.txt.encrypted
head -c 80 sample_data.txt.encrypted | xxd
```

### 3.4 Decrypt to a new path

```bash
abc data decrypt sample_data.txt.encrypted --output sample_data.roundtrip.txt
diff sample_data.txt sample_data.roundtrip.txt
```

### 3.5 Clean up

```bash
rm -f sample_data.txt sample_data.txt.encrypted sample_data.roundtrip.txt
```

---

## Exercise 4: Upload concepts

**Time target:** about 10 minutes.

TUS **resumable** uploads: endpoint and bearer token resolve from flags, then `ABC_UPLOAD_ENDPOINT` / `ABC_UPLOAD_TOKEN`, then the active context’s `upload_endpoint` / `upload_token`, then derived paths from the API endpoint.

### 4.1 Review flags

```bash
abc data upload --help
```

**Skim for:** `--endpoint`, `--upload-token`, `--meta`, `--status` / `--clear`, encryption-related flags.

### 4.2 Why TUS matters

- Large files and chunked uploads
- Pause/resume on flaky networks
- Visible progress in the client

### 4.3 Metadata (commented pattern)

When you later run a real upload, attach metadata for traceability:

```bash
# With upload_endpoint + upload_token on the active context:
# abc data upload ./sample.fastq.gz \
#   --meta researcher=alice \
#   --meta project=demo-study \
#   --meta date=$(date -u +%Y-%m-%d)
```

---

## Exercise 5: Download workflows

**Time target:** about 10 minutes.

For **`aria2`**, **`rclone`**, **`wget`**, and **`s5cmd`**, `abc data download` builds a small shell script and **always** invokes **`abc job run --submit`** under the hood so the transfer executes **on the cluster**, not on your laptop. There is **no** separate `--submit` flag on `abc data download` — you need a reachable Nomad API (and token) for those tools.

For **`nextflow`**, the same command submits a **pipeline run** via the ABC control-plane API (needs **`--url`** / context endpoint and access token, plus `--accession` or `--params-file`); that path does **not** use the Nomad wrapper above.

### 5.1 Review download flags

```bash
abc data download --help
```

**Skim for:** `--tool` (`aria2`, `rclone`, `wget`, `s5cmd`, `nextflow`), `--source` / `--url-file`, `--destination` (path **inside the task**), `--node` placement, `--driver` (`exec`, `containerd`, `docker`, …).

### 5.2 Optional: cluster download smoke

Requires Nomad reachable (`abc infra compute add` on the context, or `NOMAD_ADDR` / `NOMAD_TOKEN`). This command **submits** a download job immediately (it re-invokes `abc job run --submit` in a child process, which reads the same **`~/.abc/config.yaml`** and **`NOMAD_*` / `ABC_*` environment** as a direct `abc job run`). Use **`--region`** on **`abc job run`** / **`abc job list`** when your site needs an explicit Nomad RPC region (see [`USAGE.md`](USAGE.md) “Nomad connection flags”).

```bash
# Replace YOUR_NOMAD_NODE_NAME_OR_UUID. Prefer --driver containerd if the image provides wget.
abc data download \
  --tool wget \
  --driver containerd \
  --source https://speed.hetzner.de/100MB.bin \
  --destination /tmp/abc-demo-dl \
  --node YOUR_NOMAD_NODE_NAME_OR_UUID \
  --name demo-dl-smoke
```

After a successful run, files live on the chosen node under the task destination; chain further steps with your own `abc job run` scripts if needed.

### 5.3 Local large file for upload practice (no cluster)

Sparse **1 GiB** file for a later upload test:

```bash
# macOS — sparse 1 GiB
mkfile -n 1g ./large-sample.bin

# Linux — prefer sparse when available
if command -v truncate >/dev/null 2>&1; then truncate -s 1G ./large-sample.bin
elif command -v mkfile >/dev/null 2>&1; then mkfile -n 1g ./large-sample.bin
else dd if=/dev/zero of=./large-sample.bin bs=1M count=1024
fi
```

---

## Exercise 6: Large upload smoke

**Time target:** about 10 minutes.

### 6.1 Ensure the test file exists

If you skipped Exercise 5.3, create `./large-sample.bin` the same way as there.

### 6.2 (Optional) Encrypt before upload

Encrypting 1 GiB can take noticeable CPU; skip if you only want to exercise TUS:

```bash
abc data encrypt ./large-sample.bin --output ./large-sample.bin.encrypted
```

### 6.3 Tus settings

Prefer `upload_endpoint` / `upload_token` on the context (Exercise 1) or `ABC_UPLOAD_ENDPOINT` / `ABC_UPLOAD_TOKEN`. You can store a tus token as a secret:

```bash
abc secrets set upload-token "token-from-platform" --unsafe-local
```

### 6.4 Upload (commented; uncomment when your endpoint is live)

```bash
# abc data upload ./large-sample.bin \
#   --meta researcher=alice \
#   --meta analysis=demo-large-upload

# Or pass a token from secrets:
# abc data upload ./large-sample.bin \
#   --upload-token "$(abc secrets get upload-token --unsafe-local)" \
#   --meta analysis=demo-large-upload
```

### 6.5 Clean up

```bash
rm -f ./large-sample.bin ./large-sample.bin.encrypted
```

---

## Exercise 7: Job run and Nomad

**Time target:** about 10 minutes.

Turn **annotated shell scripts** into Nomad **batch** HCL. **Without `--submit`**, HCL is printed to stdout (**local preview**, no Nomad call). **With `--submit`**, the CLI registers the job with Nomad. (**`--dry-run`** is separate: it asks Nomad for a placement plan without registering the job; see [Next steps](#next-steps).)

**Recent CLI behaviour worth knowing:**

- **`#ABC --task-tmp`** (or CLI **`--task-tmp`**) — task-local temp: `TMPDIR` / `TMP` / `TEMP` under `${NOMAD_TASK_DIR}/tmp`, meta `abc_task_tmp`, plus a short shell preamble after the shebang. With **`--runtime=pixi-exec`** and **`--from=…/pixi.toml`**, that preamble is placed **before** the Pixi re-exec guard so temp-aware tools see the task sandbox first. See [USAGE.md — Task workspace temp](USAGE.md#task-workspace-temp).
- **`--runtime` / `--from`** — optional **Pixi** stack (`pixi-exec`, alias `pixi`); orthogonal to **`--driver`**. Not combinable with **`--driver=slurm`** (script is inline there). Full rules in [USAGE.md — Software stack](USAGE.md#software-stack-runtime-and-from).

**Nomad connectivity:** For `--submit`, set `NOMAD_ADDR` (include **`:4646`** unless documented otherwise) and `NOMAD_TOKEN`, or rely on `abc infra compute add` so the active context carries defaults.

### 7.1 ABC-native smoke script (with task-local temp)

Create **an** ABC-native script. Edit or remove the `#ABC --dc=…` line if your cluster does not use that datacenter label.

```bash
cat > ABC-smoke.sh << 'EOF'
#!/bin/bash
#ABC --name=ABC-smoke-demo
#ABC --driver=raw_exec
#ABC --task-tmp
#ABC --dc=gcp-slurm
#ABC --cores=1
#ABC --mem=128M
set -euo pipefail
echo "ABC smoke test"
echo "HOSTNAME=$(hostname)"
echo "DATE=$(date -Is)"
echo "TMPDIR=${TMPDIR:-unset}"
EOF
chmod +x ABC-smoke.sh
```

**Dry-run (no cluster required):**

```bash
abc job run ABC-smoke.sh | tee /tmp/abc-smoke.hcl
```

**What you should see:** Nomad HCL containing **`abc_task_tmp`** in **meta**, **`TMPDIR`** (and related) in the task **env** block, and a generated script body that includes the **`abc task-tmp`** shell block before your `echo` lines.

Quick checks:

```bash
grep -E 'abc_task_tmp|TMPDIR|abc task-tmp' /tmp/abc-smoke.hcl
```

**Submit** (needs Nomad):

```bash
abc job run ABC-smoke.sh --submit --watch --region global
```

### 7.2 Optional: Pixi stack (HCL only — no `--submit`)

Skip if you are short on time. This only validates **HCL generation**; a real run needs `pixi` on the task image/host and a real **`pixi.toml`** at `--from`.

```bash
cat > pixi-dryrun.sh << 'EOF'
#!/bin/bash
#ABC --name=pixi-demo-dryrun
#ABC --driver=exec
#ABC --runtime=pixi-exec
#ABC --from=/cluster/demo/pixi.toml
#ABC --task-tmp
echo hello
EOF
chmod +x pixi-dryrun.sh
abc job run pixi-dryrun.sh | grep -E 'pixi run|abc task-tmp|ABC_RUNTIME_PIXI_PHASE|abc_runtime|abc_from' | head
```

**What you should see:** `pixi run --manifest-path`, **`abc task-tmp`** lines **above** the Pixi phase guard, and meta keys for the stack.

### 7.3 SLURM-enabled job (hybrid preamble)

Script with both **`#SBATCH`** and **`#ABC`** lines (SLURM bridge path). Here we omit **`--task-tmp`**: it is most useful for Nomad sandbox drivers (`exec`, `docker`, `containerd-driver`, …); ask your operator before relying on task-local paths on **`slurm`**.

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

```bash
abc job run hybrid-slurm-smoke.sh
# abc job run hybrid-slurm-smoke.sh --submit --watch --region global
```

### 7.4 Status and cleanup

```bash
abc job list --status running
# abc job stop <job-id> --yes
rm -f ABC-smoke.sh pixi-dryrun.sh hybrid-slurm-smoke.sh /tmp/abc-smoke.hcl
```

Without a reachable Nomad (`NOMAD_ADDR` / token, or context defaults from `abc infra compute add`), `abc job list` prints a connection error — that is expected until a cluster is configured.

**Notes:** Submit paths need a reachable cluster and valid ACL token. The SLURM example needs the **`slurm`** task driver on eligible clients. **`pixi-exec`** is rejected with **`--driver=slurm`** by design.

---

## Exercise 8: More CLI commands

**Time target:** about 10 minutes.

This exercise is **help-only** — no credentials or cluster calls.

```bash
abc --help

abc pipeline run --help
abc module run --help
abc submit --help

abc infra compute --help
abc job translate --help
```

**What to notice:**

- **`pipeline run`** — Nextflow (and similar) workflows packaged for Nomad.
- **`module run`** — nf-core-style module execution via pipeline-gen integration.
- **`submit`** — dispatches by **auto-detecting** whether the target is a local job script, remote pipeline URI, or module path (see [`USAGE.md`](USAGE.md), **submit** command).
- **`infra compute`** — register Nomad endpoints and probes against workspace contexts.
- **`job translate`** — explore SLURM → ABC directive mapping without submitting.

For service operators, `abc admin services nomad cli -- …` wraps the Nomad CLI with cluster-local defaults (see [`USAGE.md`](USAGE.md)).

---

## Key takeaways

1. **Contexts:** `abc config init` on first run; then `abc auth login` or `abc context add` / `abc context use` — API endpoint, tokens, org/workspace/region labels, optional tus upload fields; optional **`contexts.<name>.cluster_type`** via `abc config set` for platform tier.
2. **Secrets:** after the working context exists, `abc secrets init --unsafe-local` for per-context crypt material; `abc secrets set/get/list --unsafe-local` for encrypted key storage (repeat **`secrets init`** when you `context use` a context that has no crypt block yet).
3. **Data at rest:** `abc data encrypt` / `abc data decrypt` share that crypt material.
4. **Upload:** TUS resumable uploads; endpoint and token from flags, env, or context.
5. **Download:** `wget` / `aria2` / `rclone` / `s5cmd` paths always run as **`abc job run --submit`** cluster jobs; **`nextflow`** uses the ABC API pipeline path instead; `--destination` is inside the task for tool downloads; `--node` pins placement.
6. **Jobs:** `abc job run` turns **`#ABC`** directives into Nomad HCL; **`--task-tmp`** / **`#ABC --task-tmp`** steer temp files into **`${NOMAD_TASK_DIR}/tmp`**; **`--runtime=pixi-exec`** + **`--from`** opt into Pixi (not with **`slurm`** driver); **`#SBATCH`** enables hybrid SLURM paths.
7. **Beyond data+jobs:** `pipeline run`, `module run`, `submit`, and `infra compute` connect the same CLI to larger workflows (Exercise 8).

---

## Next steps

- Run real uploads/downloads with your operator’s tus and Nomad settings.
- **Local HCL only (no Nomad):** `abc job run SCRIPT` (or add **`--output-file plan.hcl`**) to inspect generated resources without contacting the cluster.
- **Server-side plan (needs Nomad):** `abc job run SCRIPT --dry-run --region <nomad-rpc-region>` asks Nomad to evaluate placement **without** registering the job — this is **not** the same as omitting `--submit` (which only prints HCL locally).
- Read [`USAGE.md`](USAGE.md) for every flag and preamble directive.
- Use `abc job logs` / `abc job status` after submit to track runs.

---

## Troubleshooting

### "ABC_CRYPT_PASSWORD not set"

```bash
export ABC_CRYPT_PASSWORD="your-password"
```

Or run `abc secrets init --unsafe-local` once.

### "Cannot decrypt secret"

- Passphrase must match the one used when the secret was written (or use config-stored crypt defaults consistently).
- `abc secrets list` to confirm the key exists.

### "Upload fails without endpoint"

- Set `upload_endpoint` on the context, or `ABC_UPLOAD_ENDPOINT`, or pass `--endpoint` to `abc data upload`.
- Tus root should answer `OPTIONS` with `Tus-Version`.

### "No path to region" or other Nomad errors on `abc job run` / `abc data download`

- `NOMAD_ADDR` should include the HTTP port (**`:4646`** by default).
- **`--region global`** on `abc job run` is Nomad’s **RPC** region, not the ABC workspace `--region` on `abc context add`.
- Ask your operator for `abc infra compute add` or keep exporting `NOMAD_ADDR` / `NOMAD_TOKEN` for the session.

---

## Quick reference

| Task | Command | Notes |
|------|---------|-------|
| Add context | `abc context add <name> …` | Endpoint, tokens, org/workspace/region; optional `--upload-endpoint`; optional `cluster_type` via `abc config set` |
| Interactive login | `abc auth login` | Creates/updates a context from prompts |
| Use context | `abc context use <name>` | Switch active context |
| Show context | `abc context show` | Inspect active context |
| Init crypt defaults | `abc secrets init --unsafe-local` | Writes `contexts.<active>.crypt.password` / `crypt.salt` |
| Set secret | `abc secrets set KEY VALUE --unsafe-local` | Encrypted storage |
| Get secret | `abc secrets get KEY --unsafe-local` | Decrypt and print |
| Encrypt file | `abc data encrypt FILE` | Produces `FILE.encrypted` by default |
| Decrypt file | `abc data decrypt FILE.encrypted` | Use `--output` to pick path |
| Upload | `abc data upload FILE [--meta k=v …]` | Tus; endpoint/token from context/env/flags |
| Download | `abc data download --tool wget --source URL --destination /tmp/x [--node NODE]` | `wget`/`aria2`/`rclone`/`s5cmd`: always `abc job run --submit` (Nomad). `nextflow`: ABC API pipeline run (`--accession` or `--params-file`) |
| Job HCL (local) | `abc job run SCRIPT` | HCL to stdout; optional `--output-file` |
| Job plan (Nomad) | `abc job run SCRIPT --dry-run --region <rpc>` | Server-side evaluation; needs Nomad |
| Job submit | `abc job run SCRIPT --submit --watch --region global` | Needs Nomad |
| Task temp | `#ABC --task-tmp` or `--task-tmp` | `${NOMAD_TASK_DIR}/tmp`; see USAGE.md |
| Pixi stack | `#ABC --runtime=pixi-exec` + `#ABC --from=/path/pixi.toml` | Not with `--driver=slurm` |
| Smart dispatch | `abc submit <target>` | Auto job vs pipeline vs module |
| Get help | `abc COMMAND --help` | |
