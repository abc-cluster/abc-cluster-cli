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

## 3. Verify your workspace

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

After a few runs, ask the CLI how much accidental time it returned to you:

```bash
abc report
```

`abc report` reads `~/.abc/local.db` only — no network calls — and prints a
year-to-date personal summary: investigations explored, runs (succeeded vs.
retried), compute consumed, and an estimate of "research time saved" with a
postdoc-rate translation. See [Reference → report](./reference/abc-report) for
the full metric mapping and JSON schema.

## Trouble?

| Symptom | Try |
|---|---|
| `connect: connection refused` | You need to be on the Stellenbosch network or Tailscale VPN |
| `403 Forbidden` on submit | `abc auth context show` — confirm the **seedling** context is active and your token is set |
| Job goes to wrong namespace | `abc auth context show` — the `nomad_namespace` field in your config controls the default |
| `unknown command` | `abc --help`, then `abc <verb> --help` |

## Next steps

- [Tutorials → Hands-on walkthrough](./tutorials/demo) — five exercises covering jobs, data, pipelines, and modules.
- [Reference → job run](./reference/jobs) — every `#ABC` directive and CLI flag.
- [Reference → data](./reference/data) — `data upload`, `data download`, and object storage commands.
