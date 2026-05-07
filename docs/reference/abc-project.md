# `abc project`

Top-level grouping for investigations. Backed by `~/.abc/state.db`.

## Verbs

| Verb | Purpose |
|---|---|
| `create <title> [--description=] [--tag=]...` | Create a new project (auto-slug; sets active) |
| `list [--status=active|archived|completed] [--all] [--output=table|json|csv]` | List projects |
| `show <id> [--output=table|json]` | Detail view including investigation count |
| `use <id> | --none` | Set / clear the active project pointer |
| `status` | Show active context + active project + active investigation |
| `archive <id>` | Archive (reversible: `abc project use` reactivates) |
| `complete <id>` | Mark complete (sticky) |
| `rename <id> [--slug=] [--title=] [--description=]` | Rename or re-describe |
| `tag <id> --add=<tag> [--remove=<tag>]` | Add or remove a tag |
| `delete <id> [--force]` | Cascading delete |

## Slug rules

User-supplied slugs must match `^[a-z][a-z0-9-]{2,40}$` and be unique
within `(context_name, slug)`. Auto-generated slugs follow
`<adjective>-<noun>-<1..99>` from a curated wordlist.
