---
sidebar_position: 6
---

# job run

Convert annotated shell scripts into Nomad jobs and submit them.

## Basic usage

```bash
abc job run <script.sh> [flags]
```

`abc job run` parses `#ABC` directives from the script preamble, generates a Nomad job HCL, and submits it. Pass `--dry-run` to print the HCL without submitting.

## Key flags

| Flag | Description |
|---|---|
| `--submit` | Submit to Nomad (default: dry-run only) |
| `--task-tmp` | Mount a per-task temp directory |
| `--runtime <image>` | Container image override |
| `--from <image>` | Base image for the software stack |
| `--dry-run` | Print Nomad HCL without submitting |
| `--detach` | Return immediately, don't tail logs |
| `--namespace` | Nomad namespace (default: `default`) |

## `#ABC` preamble directives

```bash
#!/bin/bash
#ABC job_name=my-analysis
#ABC image=ubuntu:24.04
#ABC cpu=2000
#ABC memory=4096
#ABC task_tmp=true
#ABC secrets=API_KEY,DB_PASS
#ABC datacenters=dc1
```

### Full directive table

| Directive | Default | Description |
|---|---|---|
| `job_name` | script filename | Nomad job name |
| `image` | `ubuntu:24.04` | Container image |
| `cpu` | `500` | CPU MHz |
| `memory` | `512` | Memory MB |
| `task_tmp` | `false` | Mount per-task scratch volume |
| `secrets` | *(none)* | Comma-separated secret keys to inject |
| `datacenters` | `dc1,default` | Target datacenters |
| `namespace` | `default` | Nomad namespace |
| `runtime` | *(image value)* | Override runtime image |
| `from` | *(none)* | Base image for Pixi/Conda stack |

## Software stacks (Pixi / Conda)

Use `--from` or the `#ABC from` directive to activate a Pixi/Conda environment from a lockfile:

```bash
#ABC from=pixi.lock
```

`abc job run` generates a Nomad task that installs the environment before running your script.

## Job lifecycle commands

```bash
abc job list                  # list recent jobs
abc job show <id>             # detailed status
abc job stop <id>             # stop a running job
abc job logs <id>             # tail allocation logs
abc job status <id>           # short status summary
abc job dispatch <id>         # dispatch a parameterized job
abc job translate <script.sh> # print Nomad HCL only
abc job trace <id>            # trace allocation events
```
