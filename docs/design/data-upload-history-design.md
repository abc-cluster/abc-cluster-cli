# Data Upload History and Duplicate Detection Design

## Context

The CLI currently uploads files and directories via tus and can compute checksums for upload metadata. It does not persist a local history of successful uploads. Users want to answer two questions:

1. Was this file uploaded successfully before?
2. Am I uploading the same file again?

## Goals

1. Persist local upload history under $HOME/.abc/logs.
2. Detect likely duplicate uploads before and after upload attempts.
3. Keep behavior safe by default (no secrets in logs, restrictive permissions).
4. Keep implementation low risk and backward compatible.

## Non-Goals

1. Enforcing global deduplication across devices or users.
2. Replacing server-side truth for object existence.
3. Building a full analytics pipeline from CLI logs.

## Decision Summary

Use a local append-only JSON Lines history file at:

- $HOME/.abc/logs/upload-history.jsonl

Record one event per attempted upload with status fields. Primary duplicate key is:

- checksum + workspace + endpoint

Fallback duplicate key when checksum is unavailable:

- canonical_local_path + file_size + mtime_unix

The CLI should warn (not block) on duplicate detection in the first phase.

## Storage Layout

Directory and files:

- $HOME/.abc/
- $HOME/.abc/logs/
- $HOME/.abc/logs/upload-history.jsonl

Permissions:

- Directory mode: 0700
- File mode: 0600

Rotation:

- Phase 1: single file, no rotation.
- Phase 2: optional monthly rotation using upload-history-YYYY-MM.jsonl.

## Event Schema

Each line is one JSON object.

```json
{
  "timestamp": "2026-03-24T15:04:05Z",
  "command": "data upload",
  "status": "success",
  "workspace": "ws-123",
  "endpoint": "https://api.example.com/data/uploads?workspaceId=ws-123",
  "local_path": "/Users/alice/data/sample.fastq.gz",
  "relative_path": "nested/sample.fastq.gz",
  "file_name": "sample.fastq.gz",
  "file_size": 734003200,
  "mtime_unix": 1766203000,
  "checksum": "sha256:...",
  "encrypted": false,
  "upload_location": "https://uploads.example.com/files/abc",
  "error": ""
}
```

Schema notes:

1. status values: success, failed, skipped.
2. error must contain sanitized high-level messages only.
3. Never include tokens, authorization headers, or crypt secrets.

## Duplicate Detection Rules

Pre-upload warning flow:

1. Build candidate key from current file metadata.
2. Query recent history lines (Phase 1 linear scan).
3. If checksum exists and matched prior success for same workspace+endpoint, print warning.
4. If checksum is disabled, compare path+size+mtime for weaker warning.

Post-upload recording flow:

1. On success, append success event.
2. On failure, append failed event with error summary.
3. If duplicate warning occurred and user continued, still append event.

## CLI UX Proposal

New flags for data upload:

1. --history (default true): enable/disable local history write and lookup.
2. --duplicate-check (default true): enable pre-upload duplicate warning.
3. --on-duplicate values: warn|skip|fail (Phase 1 default warn; Phase 2 add skip/fail).

Environment overrides:

1. ABC_UPLOAD_HISTORY=0 disables history globally.
2. ABC_UPLOAD_DUPLICATE_CHECK=0 disables duplicate lookup globally.

Suggested warning text:

- Possible duplicate upload detected: this file checksum matches a previous successful upload on <timestamp> to workspace <workspace>. Continue upload.

## Concurrency and Reliability

Directory uploads may run in parallel, so history appends must be safe.

Approach:

1. Introduce a small history writer with process-local mutex.
2. Open file in append mode for each write and write one full line.
3. Keep writes short and fail-soft: history errors should not fail uploads.

Cross-process concurrency:

1. Phase 1: best-effort append, no OS-level lock.
2. Phase 2: optional advisory lock file if collisions are observed.

## Privacy and Security

1. Do not log access token, upload token, headers, query params containing secrets.
2. Keep absolute paths because this is local-only; document that paths are sensitive.
3. Use strict file permissions and create directories if missing.
4. Provide a cleanup command in future phase:
   - abc data history prune --older-than 90d
   - abc data history clear

## Backward Compatibility

1. Existing upload command behavior remains unchanged by default except duplicate warnings.
2. If users disable history or duplicate checks, command behaves as today.
3. No API contract changes required.

## Implementation Plan (Future)

Phase 1:

1. Add internal history package in cmd/data or internal/history.
2. Add append event calls in single-file and directory success/failure paths.
3. Add duplicate check before upload and print warning only.
4. Add unit tests for writer, parser, and duplicate matching.

Phase 2:

1. Add subcommand group: abc data history list/find/prune/clear.
2. Add on-duplicate=skip|fail modes.
3. Add optional rotation and lock file.

## Test Plan

Unit tests:

1. Creates history directory/file with expected modes.
2. Appends valid JSONL entries for success and failure.
3. Duplicate detection with checksum match and non-match.
4. Fallback detection using path+size+mtime when checksum missing.
5. Redaction checks: no secret fields logged.

Integration tests:

1. Upload same file twice with checksum enabled, expect duplicate warning on second run.
2. Upload with --checksum=false, expect weaker fallback warning behavior.
3. Parallel directory upload writes multiple records without malformed JSON lines.

Failure mode tests:

1. Non-writable HOME path should not fail upload; warning only.
2. Corrupted JSONL line should be skipped with diagnostic output (debug level).

## Open Questions

1. Should duplicate matching ignore endpoint and only use workspace?
2. Should encrypted uploads compare checksum of encrypted payload or original plaintext?
3. Do we want history enabled by default in CI/non-interactive contexts?
4. Should failed uploads be included in duplicate checks or only successes?

## Recommendation

Proceed with Phase 1 as warn-only duplicate detection and append-only local history. This delivers user value quickly with minimal behavioral risk and keeps room for stronger policies later.
