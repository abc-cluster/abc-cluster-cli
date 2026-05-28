---
sidebar_position: 2
---

# Quick start

Three steps from a fresh install to a real Nomad job. You need `abc` on your `$PATH` ([Overview](./)) and a pre-configured `~/.abc/config.yaml` handed out by your workspace lead.

## 1. Bootstrap the config directory

```bash
abc config init          # creates ~/.abc/config.yaml with a placeholder context
```

Then replace the placeholder with the YAML your workspace lead gave you:

```bash
cp ~/Downloads/<your-name>.yaml ~/.abc/config.yaml
```

## 2. Activate the seedling context

```bash
abc auth context use seedling

# Confirm the active context and your identity:
abc auth context show
abc auth whoami
```

## 3. Preflight with `abc doctor`

Before submitting anything, confirm the CLI can actually reach **and run
work on** the cluster. `abc doctor` checks your config, connectivity, and
submits a tiny probe job end-to-end:

```bash
abc doctor

# Config + connectivity only (skip the probe job):
abc doctor --skip-job
```

Exit code `0` means you're good to go. If a check fails, the output tells
you which group (config / connectivity / workload) and why — fix that
first. See [Reference → doctor](./reference/doctor) for the full check list.

## 4. Verify your workspace

One workload is baked into the CLI — no script file required:

```bash
# Randomised stress-ng job: exercises CPU, VM, and I/O stressors
abc job run hello-cluster

# Add a debug sleep to exec into the running allocation before work begins
abc job run hello-cluster --sleep=120s
```

Check that the job was submitted and watch it appear:

```bash
abc job list --status running
abc job show <job-id>
```

## See what you got

After a few runs, ask the CLI what your work cost:

```bash
# Year-to-date summary: spend + emissions
abc report

# One row per job/pipeline with cost + emissions (jobs first)
abc report runs

# Forensic view — run IDs, Nomad job IDs, exit codes, namespaces
abc report runs --full
```

`abc report` reads `~/.abc/local.db` only — no network calls — and prints a
year-to-date personal summary: investigations explored, runs (succeeded vs.
retried), compute consumed, and the period's **spend + emissions** estimate.
`abc report runs` lists each run individually; job costs are exact, pipeline
costs are pending the per-task cache aggregator (the rows are marked). See
[Reference → report](./reference/abc-report) for the full metric mapping,
the `--show-rate-card` provenance view, and the JSON schema.

## Trouble?

| Symptom | Try |
|---|---|
| Anything not working | `abc doctor` — runs config + connectivity + a probe job and tells you which layer failed |
| `connect: connection refused` | You need to be on the Stellenbosch network or Tailscale VPN |
| `403 Forbidden` on submit | `abc auth context show` — confirm the **seedling** context is active and your token is set |
| Job goes to wrong namespace | `abc auth context show` — the `nomad_namespace` field in your config controls the default |
| `unknown command` | `abc --help`, then `abc <verb> --help` |

## Next steps

- [Tutorials → Hands-on walkthrough](./tutorials/demo) — exercises covering jobs and data (submit, monitor, debug, upload, encrypt, browse).
- [Reference → job run](./reference/jobs) — every `#ABC` directive and CLI flag.
- [Reference → pipeline run](./reference/pipeline) — Nextflow pipelines on the cluster.
- [Reference → data](./reference/data) — `data upload`, `data download`, and object storage commands.
- [Reference → report](./reference/abc-report) — cost + emissions, per-run breakdowns.
