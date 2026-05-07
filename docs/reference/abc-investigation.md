# `abc investigation`

A research-oriented record primitive: every investigation is a
branchable, mergeable, annotateable thread of attempts. Backed by
`~/.abc/state.db` (see `docs/reference/local-state.md`).

## Verbs

| Verb | Purpose |
|---|---|
| `create <title>` | Create a new investigation under the active (or `--project=`) project |
| `list` | List investigations in the active project (or `--all-projects`) |
| `show [<id>]` | Show details + runs + annotations + children |
| `use <id> | --none` | Set / clear the active investigation pointer |
| `branch <parent> <title>` | Create a child branch from a parent |
| `annotate <id> [--tag=] [--note=] [--cites=<inv>:<aid>]` | Add an annotation; opens `$EDITOR` when `--note` omitted |
| `dead-end <id> --reason=` | Mark a branch as a dead-end |
| `merge <child> --into <parent> [--note=]` | Carry annotations from child to parent and mark child merged |
| `tree [<root>]` | ASCII tree of an investigation and its branches |
| `rename <id> [--slug=] [--title=]` | Rename slug or title |
| `tag <id> --add=<tag> [--remove=<tag>]` | Add or remove tags |
| `visualize [<id>] [--type=branches|timeline|flow|lineage]` | Emit Mermaid source from local SQLite |
| `export <id> --format=ro-crate|markdown|json --output=<path>` | Export to a portable bundle |
| `diff <a> <b>` | Side-by-side comparison of two investigations |

## Auto-attach

`abc pipeline run`, `abc job run`, and `abc module run` insert a row
into the local `runs` table with `project_id` and `investigation_id`
resolved per the precedence:

1. `--no-project` / `--no-investigation` → null
2. `--project=` / `--investigation=` flag
3. `ABC_PROJECT` / `ABC_INVESTIGATION` env var
4. `active_pointers` for the current cluster context
5. null

A one-line banner is printed on every submit:

```
[abc] Auto-attached: project <slug>, investigation <slug> (run RUN-…)
```

## Visualize

`abc investigation visualize` is a pure projection of local SQLite
into Mermaid source. It does not call any service. By default it
prints to stdout; pass `--output=<path>` to write a file, and
`--render=svg|png` to invoke `mmdc` (Mermaid CLI) when present on PATH.
`mmdc` is a SOFT dependency: if not present, the `.mmd` source is
written and a warning emitted to stderr instead of failing.

Four view types:

- `branches` (default) — gitGraph with branches and merges
- `timeline` — Mermaid `timeline` directive of all runs + annotations
- `flow` — `flowchart TD` chain of annotation/run nodes
- `lineage` — `flowchart LR` of investigation + citations (dotted
  arrows from the `citations` table)

Filter flags:

- `--since=YYYY-MM-DD`
- `--branches=alive|dead|all`
- `--no-runs` (annotation-only)
- `--mermaid-version=v1|v2` (default `v1` for max renderer compat)
