# abc-cluster-cli

`abc` is the command line interface for the [abc-cluster](https://abc-cluster.cloud)
platform — submit pipelines, run Nomad jobs, manage workspaces and contexts,
and inspect cluster state from the terminal.

Inspired by [tower-cli](https://github.com/seqeralabs/tower-cli); built around
HashiCorp Nomad as the orchestration substrate.

## Install

```bash
# Recommended — installer script (drops the binary in $PWD)
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh

# Install to /usr/local/bin/abc (prompts for sudo)
curl -fsSL -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh -s -- --sudo

# Or from Go
go install github.com/abc-cluster/abc-cluster-cli@latest
```

## Quick start

```bash
# 1. Initialise local config
abc config init

# 2. Add a context for your cluster
# Preferred: import the context file your workspace lead gave you
abc auth context add lab --from-file ./lab-context.yaml
abc auth context use lab

# Confirm the CLI can reach and run work on the cluster
abc doctor

# 3. Run something
abc job run hello-cluster            # built-in workload, no script file needed
abc job run hello.sh                 # your own #ABC-annotated script
abc pipeline run https://github.com/nf-core/rnaseq --revision 3.14.0

# Follow a running job
abc job logs <job-id> --follow
```

See **[docs/quickstart.md](./docs/quickstart.md)** for the full first-run walkthrough.

## Documentation

- **[docs/quickstart.md](./docs/quickstart.md)** — first-run walkthrough
- **[USAGE.md](./USAGE.md)** — full command + flag reference with examples
- **[docs/reference/](./docs/reference/)** — per-command reference pages:
  - [global-flags.md](./docs/reference/global-flags.md) — flag → env var → config mapping
  - [env-vars.md](./docs/reference/env-vars.md) — every env var the CLI knows about (auto-generated from the registry)
  - [auth-context.md](./docs/reference/auth-context.md) — context management
  - [admin.md](./docs/reference/admin.md) — operator surfaces incl. `abc admin env` introspection
  - [jobs.md](./docs/reference/jobs.md), [pipeline.md](./docs/reference/pipeline.md), [data.md](./docs/reference/data.md), [secrets.md](./docs/reference/secrets.md), [cluster.md](./docs/reference/cluster.md), [report.md](./docs/reference/abc-report.md), …
- **[DEMO.md](./DEMO.md)** — end-to-end demo scenario
- **[CHANGELOG.md](./CHANGELOG.md)** — release notes

## Inspecting the env-var surface

```bash
abc admin env list                # every canonical env var, grouped by bucket
abc admin env show ABC_API_TOKEN  # full precedence walk for one variable
abc admin env validate            # detect forbidden patterns + shadowed values
```

## Development

```bash
just check          # vet + mod verify + tests
just build          # produces ./abc
just gen            # regenerate registry-derived docs (env-vars.md)
just gen-check      # CI: fail on drift between registry and committed docs
just ci             # full pre-PR gate (fmt-check + check + gen-check)
```

Run `just --list` for the full task surface.

## Licence

Eclipse Public License 2.0. See [LICENSE](LICENSE); copyright holders are listed
in [NOTICE](NOTICE).
