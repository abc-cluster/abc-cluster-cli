# Local state — `~/.abc/state.db`

The abc CLI keeps local state in a single SQLite database at
`~/.abc/state.db`. The driver is pure Go (`modernc.org/sqlite`), so the
binary remains CGO-free.

## Tables

| Table | Purpose |
|---|---|
| `projects` | Top-level research project (slug + ULID, scoped per cluster context) |
| `investigations` | Branchable, mergeable explorations under a project |
| `annotations` | Free-form notes attached to investigations (with optional `tag`) |
| `runs` | Every pipeline / job / module submission, auto-attached to active project + investigation |
| `active_pointers` | Per-context "active project" / "active investigation" pointers |
| `cli_audit` | Per-invocation audit log |
| `citations` | Cross-investigation citation edges |
| `freezes`, `container_digests`, `pipeline_metadata`, `telemetry_queue` | Forward-compatible substrate (no consumers in this release) |

## Operational rules

- WAL mode + `busy_timeout=5000` ms; safe under concurrent CLI invocations.
- Foreign keys ON; cascading deletes on `abc project delete`.
- All write transactions use `BEGIN IMMEDIATE` semantics via the driver.
- Every CLI invocation auto-applies pending migrations.

## Admin verbs

- `abc db status` — binary version, DB path, schema version, applied/pending/future migrations, table row counts, WAL size, applied-migration history.
- `abc db migrate` — explicitly apply pending migrations.
- `abc db vacuum` — reclaim space (`VACUUM`).

## Schema versioning and migrations

The DB carries its schema version in the `schema_migrations` table. The
binary embeds a set of migration files under `internal/state/migrations/`.
On every `state.Open()` (which fires on every DB-backed verb) the CLI:

1. Compares applied migrations vs embedded migrations.
2. **DB ahead of binary** (any applied row not in the embed) — refuse to
   open with `ErrSchemaAhead` and a clear "upgrade `abc`" message. Prevents
   an old binary from operating against a future schema.
3. **Binary ahead of DB** (embedded migrations not yet applied) — write a
   pre-migration backup to `~/.abc/state.db.backup-pre-<version>-<unix>`,
   then apply each pending migration in its own IMMEDIATE transaction.
   The most recent 5 backups are retained automatically; older ones pruned.
4. **Equal** — no-op.

Each `schema_migrations` row records the CLI version that applied it
(`applied_by_cli_version`), so `abc db status` shows the full provenance
chain.

### Adding a new migration

1. Create `internal/state/migrations/NNNN_short_description.sql` where
   `NNNN` is one greater than the current highest migration filename. Use
   `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE … ADD COLUMN` for
   forward-only changes.
2. Migrations are forward-only; **never edit a migration file once shipped**.
   To revert, write a new migration that walks back the change.
3. Bump the CLI version in the next release (`-ldflags "-X cmd.version=…"`)
   so the audit trail is meaningful.
4. Add or update unit tests in `internal/state/migrations/migrations_test.go`
   to exercise the new schema.

### Recovering from a failed migration

If a migration fails mid-apply, the IMMEDIATE transaction rolls back, the
DB stays at the pre-migration version, and the error message points at
the backup file written before the attempt:

```
~/.abc/state.db.backup-pre-NNNN_<name>-<unix>
```

To restore: stop all `abc` processes, replace `~/.abc/state.db` with the
backup, then re-run `abc db status` to confirm the schema version.

## Backup

`~/.abc/state.db` is a single file. Copy it (with the `-wal` and `-shm`
sidecars while idle) to back up. The CLI never writes anything cluster-side
that depends on this file. Pre-migration backups (above) are an automatic
form of this for the migration boundary specifically.
