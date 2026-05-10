# Local state — `~/.abc/local.db`

The abc CLI keeps local state in a single SQLite database at
`~/.abc/local.db`. The driver is pure Go (`modernc.org/sqlite`), so the
binary remains CGO-free.

> **Renamed 2026-05-08:** the file was previously `~/.abc/state.db`. On
> first invocation post-upgrade, the CLI auto-renames the legacy file +
> WAL/SHM sidecars + any `state.db.backup-pre-*` files in place, prints
> a one-line stderr note, and continues. No manual action required.

## Tables

| Table | Purpose |
|---|---|
| `projects` | Top-level research project (slug + ULID, scoped per cluster context) |
| `investigations` | Branchable, mergeable explorations under a project |
| `annotations` | Free-form notes attached to investigations. Tags stored as a JSON array in `tags_json`; soft-delete via `withdrawn_at` / `withdrawn_reason`. The legacy singular `tag` column is preserved (additive migration) for cross-version readers. |
| `annotation_revisions` | Audit trail for every annotation mutation. One row per body edit / tag change / move / withdraw / restore, capturing the previous state of whichever field changed. Walked oldest→newest to reconstruct history. |
| `runs` | Every pipeline / job / module submission, auto-attached to active project + investigation. Carries `cpu_hours`, `memory_gb_hours`, `walltime_seconds`, `gpu_count`, `scratch_gb` (transient scratch reservation), `exit_code`, and `exit_reason` for cost / emissions reporting (`abc report`). The completion fields are written by the run-watcher (`internal/runner/watcher.go`) — best-effort, polled from Nomad every 5 s up to a 24 h ceiling. |
| `active_pointers` | Per-context "active project" / "active investigation" pointers |
| `cli_audit` | Per-invocation command log: verb, redacted argv, exit code, duration ms, user ULID. Set `ABC_NO_AUDIT=1` to disable. Feeds `abc report`. |
| `citations` | Cross-investigation citation edges (populated by `abc project investigation annotate --cites=<inv>:<aid>`). |
| `freezes`, `container_digests`, `pipeline_metadata`, `telemetry_queue` | Forward-compatible substrate (no consumers in this release) |

## Operational rules

- WAL mode + `busy_timeout=5000` ms; safe under concurrent CLI invocations.
- Foreign keys ON; cascading deletes on `abc project delete`.
- All write transactions use `BEGIN IMMEDIATE` semantics via the driver.
- Every CLI invocation auto-applies pending migrations.

## Admin verbs

- `abc localdb status` — binary version, DB path, schema version, applied/pending/future migrations, table row counts, WAL size, applied-migration history.
- `abc localdb migrate` — explicitly apply pending migrations.
- `abc localdb vacuum` — reclaim space (`VACUUM`).

> The `abc cache` group is the deprecated former name of `abc localdb`. It
> remains as an alias for one release and prints a one-line deprecation note
> at invocation; new scripts should use `abc localdb`.

## Schema versioning and migrations

The DB carries its schema version in the `schema_migrations` table. The
binary embeds a set of migration files under `internal/state/migrations/`.
On every `state.Open()` (which fires on every DB-backed verb) the CLI:

1. Compares applied migrations vs embedded migrations.
2. **DB ahead of binary** (any applied row not in the embed) — refuse to
   open with `ErrSchemaAhead` and a clear "upgrade `abc`" message. Prevents
   an old binary from operating against a future schema.
3. **Binary ahead of DB** (embedded migrations not yet applied) — write a
   pre-migration backup to `~/.abc/local.db.backup-pre-<version>-<unix>`,
   then apply each pending migration in its own IMMEDIATE transaction.
   The most recent 5 backups are retained automatically; older ones pruned.
4. **Equal** — no-op.

Each `schema_migrations` row records the CLI version that applied it
(`applied_by_cli_version`), so `abc localdb status` shows the full provenance
chain.

### Migration authoring rule

Migrations are **forward-only**. The rule has three parts:

1. **Never edit a shipped migration file.** Once a `0NNN_*.sql` has been
   applied to any user's `local.db`, its content is frozen. To change the
   schema, write a new migration that walks the schema forward.
2. **New migration filename = `NNNN_<short_description>.sql`** where `NNNN`
   is one greater than the current highest filename in
   `internal/state/migrations/`. Use `CREATE TABLE IF NOT EXISTS`,
   `ALTER TABLE … ADD COLUMN`, and `CREATE INDEX IF NOT EXISTS` so the
   migration is idempotent against partial replays.
3. **The numeric sequence may have gaps** — `0003` is intentionally absent
   (it was renumbered to `0005` historically). The migration driver applies
   files in lexicographic order from whatever set is embedded; gaps are
   harmless. Do NOT renumber existing files to "fill" the gap.

After writing the migration, add or update unit tests in
`internal/state/migrations/migrations_test.go` to exercise the new schema,
and bump the CLI version in the next release (`-ldflags "-X cmd.version=…"`)
so the audit trail records which binary applied which migration.

### Recovering from a failed migration

If a migration fails mid-apply, the IMMEDIATE transaction rolls back, the
DB stays at the pre-migration version, and the error message points at
the backup file written before the attempt:

```
~/.abc/local.db.backup-pre-NNNN_<name>-<unix>
```

To restore: stop all `abc` processes, replace `~/.abc/local.db` with the
backup, then re-run `abc localdb status` to confirm the schema version.

## Backup

`~/.abc/local.db` is a single file. Copy it (with the `-wal` and `-shm`
sidecars while idle) to back up. The CLI never writes anything cluster-side
that depends on this file. Pre-migration backups (above) are an automatic
form of this for the migration boundary specifically.
