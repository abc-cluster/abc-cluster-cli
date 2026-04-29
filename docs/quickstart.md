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

## 2. Activate the aither context

```bash
abc context use aither

# Confirm the active context and your identity:
abc context show
abc auth whoami
```

## 3. Verify your workspace

One workload is baked into the CLI — no script file required:

```bash
# Randomised stress-ng job: exercises CPU, VM, and I/O stressors
abc job run hello-abc

# Add a debug sleep to exec into the running allocation before work begins
abc job run hello-abc --sleep=120s
```

Check that the job was submitted and watch it appear:

```bash
abc job list --status running
abc job show <job-id>
```

## Trouble?

| Symptom | Try |
|---|---|
| `connect: connection refused` | You need to be on the Stellenbosch network or Tailscale VPN |
| `403 Forbidden` on submit | `abc context show` — confirm the **aither** context is active and your token is set |
| Job goes to wrong namespace | `abc context show` — the `nomad_namespace` field in your config controls the default |
| `unknown command` | `abc --help`, then `abc <verb> --help` |

## Next steps

- [Tutorials → Hands-on walkthrough](./tutorials/demo) — five exercises covering jobs, data, pipelines, and modules.
- [Reference → job run](./reference/jobs) — every `#ABC` directive and CLI flag.
- [Reference → data](./reference/data) — `data upload`, `data download`, and object storage commands.
