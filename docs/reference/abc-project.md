# `abc project`

Top-level grouping for investigations. Backed by `~/.abc/local.db`.

## Verbs

| Verb | Purpose |
|---|---|
| `create <title> [--name=] [--description=] [--tag=]...` | Create a new project (auto-name if omitted; sets active) |
| `list [--status=active|archived|completed] [--all] [--output=table|json|csv]` | List projects |
| `show <name-or-id> [--output=table|json]` | Detail view including investigation count |
| `use <name-or-id> \| --none` | Set / clear the active project pointer |
| `status` | Show active context + active project + active investigation |
| `archive <name-or-id>` | Archive (reversible: `abc project use` reactivates) |
| `complete <name-or-id>` | Mark complete (sticky) |
| `rename <name-or-id> [--name=] [--title=] [--description=]` | Rename or re-describe |
| `tag <name-or-id> --add=<tag> [--remove=<tag>]` | Add or remove a tag |
| `delete <name-or-id> [--force]` | Cascading delete |

## Name rules

User-supplied names must match `^[a-z][a-z0-9-]{2,40}$` and be unique
within `(context_name, name)`. Auto-generated names follow
`<adjective>-<noun>-<1..99>` from a curated wordlist.
