---
sidebar_position: 7
---

# data

Manage the full data lifecycle on abc-cluster: move data between your machine and
cluster storage (MinIO), acquire data from external sources, share within your group,
delete safely, and run S3 plumbing operations.

Credentials and the S3 endpoint are always resolved from the active context — you
never pass raw S3 credentials to any command.

The surface is organised in two layers:

- **Porcelain** — high-level, task-oriented commands.
- **Plumbing** — thin wrappers over local tools (`s5cmd`, `mcli`, `aria2c`, `rclone`)
  that mirror the AWS S3 / s5cmd surface. All flags after the arguments pass through
  to the underlying tool verbatim.

---

## Moving data: you ↔ cluster storage

| Command | Direction | Tool | Notes |
|---|---|---|---|
| `abc data upload <file>` | local → MinIO | tus | resumable, tracked across CLI restarts; best on unreliable networks |
| `abc data download <s3-uri>` | MinIO → local | aria2c + presigned URL | resumable; no raw S3 creds on the client |
| `abc data push <file> <s3-uri>` | local → MinIO | s5cmd | fast, parallel; no tracking |
| `abc data pull <s3-uri>` | MinIO → local | s5cmd | fast, checksum-verified |

```bash
# Resumable upload (survives interruption — re-run to resume):
abc data upload ./genome.fa

# Fast push of a directory:
abc data push ./results/ s3://su-mbhg-hostgen/user/calm-dassie/results/

# Pull results back, checksum-verified (re-run skips unchanged files):
abc data pull s3://su-mbhg-hostgen/user/calm-dassie/results/ --destination ./out/
```

`push` flags: `--parallel <n>` (workers), `--checksum` (skip files whose checksum
matches the remote), `--dry-run`.

---

## Compress & decompress (zstd)

| Command | What it does |
|---|---|
| `abc data compress <path>` | zstd-compress a raw file/folder to `.zst` (BGZF-aware); already-compressed inputs pass through |
| `abc data decompress <path>` | expand a `.zst` (or any stock zstd) file/folder, verifying integrity |

zstd compresses raw genomic text well (≈8–10× on real VCF, ≈25–30× on TSV/TXT).
Already-compressed inputs — gzip, **BGZF** (`.bam`, tabix-indexed `.vcf.gz`), zstd —
are detected by magic bytes and **passed through unchanged**; recompressing BGZF
would break tabix random access. Each `.zst` embeds an integrity frame (original
name, size, SHA-256), so `decompress` verifies the result and `compress --replace`
deletes the source only after a verified round-trip. Output is readable by stock
`zstd -d`.

```bash
# Compress a raw VCF; keep only the .zst once verified:
abc data compress ./calls.vcf --level best --replace

# Restore it (integrity-checked):
abc data decompress ./calls.vcf.zst
```

The same capability is available inline on the transfer commands:
`abc data upload --compress` (compresses **before** encryption),
`abc data push --compress`, and `abc data pull --decompress`.

---

## Acquiring data: external → cluster

| Command | Source | Mechanism |
|---|---|---|
| `abc data fetch <url\|accession>` | HTTP(S) URL, public S3, or accession (SRR, GSE, GCF_, …) | server-side Nomad job |
| `abc data download <accession>` | scientific database by accession | redirects to `fetch` |

```bash
# Fetch a file from the internet into your MinIO bucket (runs on the cluster):
abc data fetch https://example.com/data.tar.gz

# Fetch a public sequence dataset by accession (auto-detects SRA → nf-core/fetchngs):
abc data fetch SRR000001
```

---

## Within the cluster

| Command | What it does |
|---|---|
| `abc data stage <s3-uri>` | copy a MinIO object into your workbench `~/data/` (Nomad job on the platform node) |
| `abc data share <s3-uri> --to shared` | server-side copy into your group's `shared/<your-slot>/` |
| `abc data share <s3-uri> --to common` | server-side copy into `common/` (admin only) |
| `abc data presign <s3-uri>` | generate a time-limited presigned URL for any accessible object |

```bash
# Make a MinIO file appear in your JupyterLab ~/data/ folder:
abc data stage s3://su-mbhg-hostgen/user/calm-dassie/genome.fa

# Share a result with your group (appears in everyone's ~/shared/ within ~10s):
abc data share s3://su-mbhg-hostgen/user/calm-dassie/results.vcf --to shared

# Hand an external collaborator a 48-hour download link (no cluster account needed):
abc data presign s3://su-mbhg-hostgen/user/calm-dassie/report.pdf --expires 48h
```

`presign` flags: `--expires <duration>` (e.g. `30m`, `4h`, `48h`; max 7d), `--method GET|PUT`.

---

## Deletion — three tiers

Deletion has three tiers with increasing finality. Choose by intent:

> **Naming (changed 2026-06-05):** `delete`/`del` is now the *safe, recoverable*
> verb (→ trash, like a desktop "move to Trash"), and `remove`/`rm` is the
> *permanent* one (like Unix `rm`). This inverts the earlier mapping — see
> `abc-universe` brainstorm `abc-data-platform/2026-06-05-invert-remove-delete.md`.

| Command | Alias | Tier | Reversible? | Confirmation |
|---|---|---|---|---|
| `abc data delete <s3-uri>` | `del` | soft delete to trash | **Yes** (until trash expiry) | none |
| `abc data remove <s3-uri>` | `rm` | permanent, current version | No | prompts (`--yes` to skip) |
| `abc data purge <s3-uri>` | — | permanent, **all** versions | No | must type `purge` (`--yes` to skip) |

### delete (del) — the safe default

Moves the object to `s3://<bucket>/trash/<your-slot>/<original-key>`. Recoverable
with `abc data trash restore` until the trash lifecycle rule expires it (default 30
days).

```bash
abc data delete s3://su-mbhg-hostgen/user/calm-dassie/old.vcf
# → trashed: …/old.vcf → s3://su-mbhg-hostgen/trash/calm-dassie/user/calm-dassie/old.vcf
```

`--overwrite` replaces an object of the same name already in trash (relevant on
unversioned buckets).

### remove (rm) — permanent, current version

Permanently removes the current version (Unix `rm` convention). Does **not** go
through trash. Prompts for confirmation unless `--yes`.

```bash
abc data remove s3://su-mbhg-hostgen/user/calm-dassie/scratch.bam
abc data remove s3://su-mbhg-hostgen/user/calm-dassie/tmp/ --recursive --yes
```

Flags: `--yes` (skip prompt), `--recursive`, `--dry-run`.

### purge — complete erasure, all versions

Removes **all** versions of an object (or prefix), including delete markers — the
operation that satisfies a GDPR/POPIA right-to-erasure request. Requires typing the
word `purge` to confirm. On an unversioned bucket this is equivalent to `remove`
(the CLI says so explicitly).

```bash
abc data purge s3://su-mbhg-hostgen/user/calm-dassie/cohort-a/sample01.vcf
# WARNING: purge permanently erases ALL versions … Type 'purge' to confirm:
```

Flags: `--yes` (skip the typed confirmation), `--dry-run`.

### trash — manage soft-deleted objects

```bash
abc data trash list                          # what's in your trash
abc data trash restore <trash-s3-uri>        # move back to the original key
abc data trash empty [--yes]                 # permanently delete all of your trash
```

`trash restore` recovers the original key by stripping `trash/<slot>/` from the trash
path, so the object returns to exactly where it was. `--overwrite` replaces the
original location if it already exists.

---

## Plumbing (S3 / s5cmd surface)

Thin wrappers; flags after the arguments pass through to the underlying tool.
Canonical names are full English words; the familiar short forms are registered
aliases.

| Canonical | Alias | Wraps | Purpose |
|---|---|---|---|
| `list` | `ls` | s5cmd ls | list objects or buckets |
| `sync` | — | s5cmd sync | sync two prefixes or directories |
| `cat` | — | s5cmd cat | stream an object to stdout |
| `pipe` | — | s5cmd pipe | upload stdin to an object |
| `disk-usage` | `du` | s5cmd du | storage usage for a prefix |
| `make-bucket` | `mb` | s5cmd mb | create a bucket |
| `remove-bucket` | `rb` | s5cmd rb | remove a bucket |
| `stat` | — | head-object | object metadata |
| `copy` | `cp` | rclone (Nomad job) | server-side copy |
| `move` | `mv` | rclone (Nomad job) | server-side move |

```bash
abc data list s3://su-mbhg-hostgen/user/calm-dassie/
abc data cat s3://su-mbhg-hostgen/user/calm-dassie/manifest.json | jq .
samtools view -bS in.sam | abc data pipe s3://su-mbhg-hostgen/user/calm-dassie/out.bam
abc data du s3://su-mbhg-hostgen/user/calm-dassie/

# Pass flags through to the underlying tool:
abc data list s3://su-mbhg-hostgen/ --show-fullpath
```

### Tool resolution

Each plumbing command locates its tool in this order:

1. `ABC_BIN_<TOOL>` environment variable (e.g. `ABC_BIN_S5CMD`)
2. `~/.abc/binaries/<tool>` (managed by `abc operator tools fetch`)
3. system `PATH`

If the tool is missing, the command fails with an install hint. `mcli` is detected
even when installed as `mc`, by verifying it is MinIO Client (not GNU Midnight
Commander, which shares the `mc` name on some systems).

---

## Encrypt / decrypt

Encrypt or decrypt a local file without uploading:

```bash
abc data encrypt <file>           # → <file>.enc
abc data decrypt <file>.enc       # → <file>
```

Uses the crypt key material from the active context. Share your public key with
collaborators who need to encrypt data for you.
