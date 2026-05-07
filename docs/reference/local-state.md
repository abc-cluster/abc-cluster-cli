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

- `abc db status` — DB path, schema version, table row counts, WAL size.
- `abc db migrate` — explicitly apply pending migrations.
- `abc db vacuum` — reclaim space (`VACUUM`).

## Backup

`~/.abc/state.db` is a single file. Copy it (with the `-wal` and `-shm`
sidecars while idle) to back up. The CLI never writes anything cluster-side
that depends on this file.
