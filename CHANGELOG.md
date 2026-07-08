# Changelog

All notable changes to `abc-cluster-cli` are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions before v0.1.40 are
not individually documented — see `git tag` for the full history.

## v0.1.70 — 2026-07-08
- `abc job run`: unconstrained runs now default `node_pool` to `worker_pool`
  ("compute") instead of `head_pool` ("platform") — an ad-hoc job script is
  the workload itself (the analog of a pipeline's worker tasks), not a
  lightweight orchestrator, so it belongs on the compute pool. Previously
  every unconstrained `abc job run` on seedling-prod landed only on aither
  (the sole platform-pool node) instead of the compute pool
  (nomad01/02/03, oci-af).
- Fixed the generated `docs/` site's `url`/`baseUrl` (`docusaurus.config.ts`)
  — hardcoded a domain that has never resolved, so every canonical link,
  Open Graph tag, and Twitter card on the live docs site pointed at a dead
  host instead of `seedling.abc-cluster.cloud`.
- Public seedling docs no longer mention the grove/garden tiers — neither
  is shipped, and referencing them (a comparison table, a "Grove-tier
  roadmap" section, dangling links to already tier-hidden pages) leaked
  unshipped roadmap content to seedling-tier readers.
- Removed the compiled-in default API endpoint (`https://api.abc-cluster.io`,
  also never resolved) that `EnsureDefaultContext()` seeded into every fresh
  `config.yaml`, `abc auth login`'s prompt fell back to, and the `data` API
  client would attempt to hit. Real onboarded users are unaffected (the
  seedling claim-code flow stamps a working endpoint directly and never
  touches this path); a cold `abc config init`/`abc auth login` with no
  prior config now gets a clear "required"/"empty" error instead of a DNS
  failure against a domain that was never registered.

## v0.1.69 — 2026-07-06
- `abc pipeline run --node`: the head job's node constraint now uses
  `node.unique.name` instead of the OS-level hostname, matching the attribute
  `--pin-workers` and `--worker-exclude-host` already used. Previously a
  single `--node` value couldn't place both head and workers on any node
  whose OS hostname differs from its Nomad node name (e.g. oci-af).
- `--worker-exclude-host` is now repeatable (previously a later value silently
  replaced an earlier one).
- `--pin-workers` no longer silently drops the worker job's `nodePool` line,
  which left per-task Nomad jobs on the zero-node `default` pool.
- S3-workdir pipeline workers now mount `nf-work` (RW on every node) instead
  of `abc-tools` as the `/nxf-work` tools carrier, fixing worker registration
  on nodes where `abc-tools` is registered read-only (e.g. aither, oci-af).
- `deployments/abc-nodes/clients/aither-client.hcl` resynced with aither's
  live single-node server+client config — the checked-in file had drifted
  since a 2026-05-07 architecture change and was missing 7 of 8 registered
  host volumes.
- Fixed two `abc app deploy` bugs for data-bucket-backed apps: a missing
  explicit secret key on MinIO service-account creation, and generated
  access-key names exceeding MinIO's 20-character limit.

## v0.1.68 — 2026-07-03
- `abc auth whoami` now caches the Nomad ACL token type
  (`admin.services.nomad.token_type`) and shows it alongside role/group —
  a `management` token bypasses all Nomad ACL policy checks, so
  `group: (none)` on such a context is now flagged as expected rather than
  left ambiguous.

## v0.1.67 — 2026-07-02
- `abc pipeline run` fails fast with an actionable error when a group-less
  user's S3 work-dir would silently derive an inaccessible
  `s3://nextflow-work/` bucket, instead of a confusing Nextflow retry-storm.
- `abc pipeline run --wait --logs` no longer dies with `400 unknown task
  name "main"` when the alloc's task states aren't populated yet —
  resolves the real head task name (`nf-task`/`nextflow`) deterministically.

## v0.1.66 — 2026-07-02
- Pipeline workers now mount `abc-tools` read-only when a work-dir host volume is also
  present (non-S3 pipelines), matching ADR-0061. S3-workdir pipelines still mount it
  read-write (tracked as an open follow-up — see ADR-0061).

## v0.1.65 — 2026-06-30
- Bump pinned nf-nomad `0.4.3` → `0.4.4` (fixes `restart-attempts-0` handling)

## v0.1.64 — 2026-06-30
- Pin `nf-nomad`/`nf-nomad-s5cmd` to stable releases; bump bundled Nextflow to `26.04.3`

## v0.1.63 — 2026-06-27
- Managed uploads now stamp `abc-group` + `abc-key-version` on stored objects, so files
  self-describe which key encrypted them (supports future key rotation)

## v0.1.62 — 2026-06-27
- Fixed upload token exchange for `seedling/v1` contexts (opaque token → Nomad token)

## v0.1.61 — 2026-06-27
- Upload encryption migrated to native age (X25519) — see managed encryption below

## v0.1.60 — 2026-06-27
- Managed encryption: dropped the `--group` flag — the active context is now the sole
  source of truth for which group's key is used

## v0.1.59 — 2026-06-26
- **Managed age encryption.** `abc data encrypt`/`decrypt`/`upload` gained a managed mode
  backed by native age X25519 keys (broker-issued, per-group). Passphrase encryption
  remains available as an alternative. rclone-crypt is no longer the encryption path.

## v0.1.58 — 2026-06-26
- Pipeline runs read `s5cmd` from the `abc-tools` volume instead of downloading it per run

## v0.1.57 — 2026-06-12
- `abc app`: per-deployment ingress resolved from context config (no more
  environment-specific hardcoding)

## v0.1.56 — 2026-06-12
- `abc app list`: full clickable URLs for private/shared apps

## v0.1.55 — 2026-06-12
- `abc app`: `stripPrefix` middleware support for custom-framework private/shared apps;
  dry-run placement warnings

## v0.1.54 — 2026-06-12
- **App exposure planes.** `expose:` now accepts a scalar or array in pipeline/job YAML;
  apps are addressed uniformly at `/apps/<app>`
- Fixed an `abc data` round-trip bug

## v0.1.53 — 2026-06-10
- `abc app`: bridge networking with dynamic ports (no port collisions); configurable
  health-check timeout

## v0.1.52 — 2026-06-08
- `abc pipeline`: `--env`/`--git-token` flags; `abc job logs` auto-resolves the task name;
  `s5cmd` served from the `abc-tools` volume; concurrent-submit throttling

## v0.1.51 — 2026-06-07
- Documented `abc data compress`/`decompress`; fixed stale download tests and download docs

## v0.1.50 — 2026-06-07
- `abc data compress`/`decompress` (zstd); `--compress` on upload/push, `--decompress` on pull

## v0.1.49 — 2026-06-07
- User-secret portability: broker secrets backend, `cred_source`-driven crypt password

## v0.1.48 — 2026-06-06
- `abc whoami`: identity-only output (Nomad is no longer surfaced as an implementation detail)

## v0.1.47 — 2026-06-06
- `abc whoami` UX redesign; upload/push clarified (both verbs kept, distinct purposes)

## v0.1.46 — 2026-06-06
- Renamed the space-saving encrypt/decrypt flag: `--remove-source` → `--replace`

## v0.1.45 — 2026-06-06
- Smart trailing-`/` handling on `list`; content-hash `--force`; `--remove-source` flag

## v0.1.44 — 2026-06-06
- `abc data encrypt`/`decrypt`: no more `.dec` suffix; refuses to silently clobber
  existing files; added `--force`

## v0.1.43 — 2026-06-06
- `abc admin`: `tools.toml` auto-initializes on first run

## v0.1.42 — 2026-06-06
- SDK-based control plane (phases 1–3): `presign`/`ls`/`stat`/`du` via `minio-go`

## v0.1.41 — 2026-06-05
- `abc data` command surface cleanup

## v0.1.40 — 2026-06-04
- **`abc data send`** — ephemeral, expiring file transfer; upload progress bar
