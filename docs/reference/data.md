---
sidebar_position: 7
---

# data

Upload, download, encrypt, and decrypt files against the cluster's object storage.

## data upload

Resumable upload via TUS protocol:

```bash
abc data upload <file> [flags]
```

| Flag | Description |
|---|---|
| `--encrypt` | Encrypt before upload using local crypt key |
| `--bucket` | Target S3 bucket |
| `--key` | Override object key (default: filename) |
| `--chunk-size` | TUS chunk size in bytes |
| `--resume` | Resume an interrupted upload |

TUS uploads survive network interruptions. If a large upload is interrupted, re-run the same command — it will resume from the last completed chunk.

## data download

```bash
abc data download <key> [--output <file>] [--decrypt]
```

`--decrypt` automatically decrypts if the object was uploaded with `--encrypt`.

## data encrypt / decrypt

Encrypt or decrypt a local file without uploading:

```bash
abc data encrypt <file>           # → <file>.enc
abc data decrypt <file>.enc       # → <file>
```

Uses the age key pair from `abc secrets init`. Share the public key with collaborators who need to encrypt data for you.

## Large files

For very large files (tens of GB), use `--chunk-size` to tune TUS chunk size:

```bash
abc data upload large-dataset.tar.gz \
  --encrypt \
  --chunk-size 104857600   # 100 MB chunks
```
