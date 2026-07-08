# `abc doctor`

Verifies that the CLI is correctly configured and can reach **and submit
work to** the active cluster. `abc doctor` starts from your
`~/.abc/config.yaml` and proves the full client→alloc path end to end —
the check you run first when something isn't working.

## Usage

```
abc doctor                      # all three check groups
abc doctor --skip-job           # config + connectivity only (no probe job)
abc doctor --context=seedling   # check a named context, not the active one
abc doctor --json               # machine-readable result object
abc doctor --job-timeout=2m     # allow longer for the probe job to complete
```

## Check groups

Three groups run in order; a failure in an earlier group still lets later
groups run (so you see the full picture, not just the first error).

| # | Group | What it checks |
|---|---|---|
| 1 | **Config** | `config.yaml` loads, an active context is set, the Nomad address and access token are present. |
| 2 | **Connectivity** | Nomad is reachable and reports healthy. MinIO/S3 is probed when the active context has an S3 endpoint configured. |
| 3 | **Workload** | Submits a minimal `raw_exec` batch probe job, waits up to the job timeout (default 60 s) for it to complete, then purges it. Proves submission + scheduling + completion actually work, not just connectivity. Skip with `--skip-job`. |

## Flags

| Flag | Default | Description |
|---|---|---|
| `--skip-job` | `false` | Run groups 1 and 2 only — skip the workload probe. Useful on a constrained cluster or when you only want to validate config + reachability. |
| `--context=<name>` | active context | Run the checks against a named context instead of the active one. |
| `--json` | `false` | Emit results as a single JSON object (one entry per check with pass/fail + detail). |
| `--job-timeout=<dur>` | `1m` | Maximum time to wait for the probe job to complete before declaring the workload check failed. |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | All run checks passed. |
| `1` | One or more checks failed. |

## See also

- [Global flags](./global-flags) — `--context`, `--json`, `--quiet`
