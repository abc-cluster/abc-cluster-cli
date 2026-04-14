# `abc job run` — Comprehensive Test Plan

> **Purpose:** Exhaustive test coverage specification for `abc job run`.
> Tests are grouped by scenario class, each tagged with the execution mode (offline / integration).
> Offline tests run anywhere with `go test ./cmd/job/...`.
> Integration tests require a live Nomad endpoint and run with `go test -tags integration ./cmd/job/...`.

---

## Execution Modes

| Mode | Build tag | What it tests | Nomad required? |
|------|-----------|---------------|-----------------|
| **Offline / unit** | *(none)* | HCL generation, preamble parsing, flag handling, error cases | No |
| **Integration** | `integration` | Live submission, plan, log streaming, job lifecycle | Yes — `NOMAD_ADDR` + `NOMAD_TOKEN` env vars |

---

## Section A — Offline Unit Tests

### A.1 Driver: `exec` (default)

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.1.1 | `#NOMAD` preamble → exec driver, template block, bash command | ✅ covered |
| A.1.2 | `#ABC` preamble basic (name, nodes, cores, mem) | ✅ covered |
| A.1.3 | No preamble at all — defaults only, name from filename | ✅ covered (DefaultNameFromFilename) |
| A.1.4 | GPU request `#ABC --gpus=2` → `device "nvidia/gpu" { count = 2 }` | **new** |
| A.1.5 | `--chdir=/data` directive → `config { work_dir = "/data" }` in HCL | **new** |
| A.1.6 | `--depend=complete:other-job` → `lifecycle { hook = "prestart" }` with upstream job ID | **new** |
| A.1.7 | `#ABC --pixi` (boolean, no value) → `abc_pixi = "true"` in meta block | **new** |
| A.1.8 | `#ABC --pixi=true` → error: pixi does not accept a value | **new** |
| A.1.9 | Script with no shebang line (bare commands) → exec driver, HCL still valid | **new** |
| A.1.10 | Job name sanitization — filename with spaces/dots becomes safe Nomad ID | **new** |
| A.1.11 | `--output-file /path/out.hcl` writes HCL to file, nothing on stdout | **new** |

### A.2 Driver: `slurm`

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.2.1 | `#SBATCH` auto-detection → `driver = "slurm"` | ✅ covered |
| A.2.2 | `#SBATCH --output / --error / --chdir` → stdout_file, stderr_file, work_dir | ✅ covered |
| A.2.3 | Inline script escaping — `${VAR}` → `$${VAR}` | ✅ covered |
| A.2.4 | `#ABC --cores` overrides `#SBATCH --cpus-per-task` | ✅ covered |
| A.2.5 | `#ABC --driver=exec` in hybrid script overrides slurm | ✅ covered |
| A.2.6 | `#SBATCH --account=bio_team` → `account = "bio_team"` in slurm config | **new** |
| A.2.7 | `#SBATCH --ntasks=8` → `ntasks = 8` in slurm config | **new** |
| A.2.8 | `#SBATCH` only, no `--job-name` → name derived from script filename | **new** |
| A.2.9 | Walltime with slurm driver — `--time=01:00:00` does NOT emit `timeout` wrapper | ✅ covered (indirect) |
| A.2.10 | `--preamble-mode=slurm` with `#ABC` only → error (requires `#SBATCH`) | ✅ covered |

### A.3 Driver: `docker`

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.3.1 | `--driver.config.image` + `--driver.config.volumes` → docker config block | ✅ covered |
| A.3.2 | `--driver=docker --driver.config.image=...` CLI override | **new** |
| A.3.3 | Docker with resources (cores + mem) → resources block present | **new** |

### A.4 Driver: `hpc-bridge`

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.4.1 | `#ABC --driver=hpc-bridge` → `driver = "hpc-bridge"` | **new** |
| A.4.2 | hpc-bridge + SLURM partition passed through | **new** |

---

### A.5 Preamble Parsing

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.5.1 | Directives stop at first non-comment body line | ✅ covered |
| A.5.2 | `--preamble-mode=abc` ignores all `#SBATCH` lines | ✅ covered |
| A.5.3 | `#NOMAD` + `#ABC` in same script — `#ABC` wins | ✅ covered |
| A.5.4 | Inline comment stripped — `#ABC --name=test # this is a comment` → name=test | **new** |
| A.5.5 | Empty script (shebang only) → no directives, all defaults apply | **new** |
| A.5.6 | `#!` shebang line is not treated as a directive | **new** (implicit, but explicit test) |

---

### A.6 Directive Merging — Precedence

Full precedence stack: **CLI flags > #ABC > #NOMAD > NOMAD_* env vars > params file**

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.6.1 | `--name` CLI overrides `#ABC --name` which overrides `#NOMAD --name` | ✅ covered (ABCOverridesNOMAD, ParamsAndCLIOverridePriority) |
| A.6.2 | NOMAD_JOB_NAME env var used when no preamble name given | ✅ covered |
| A.6.3 | NOMAD_CPU_CORES env var used when no preamble cores given | ✅ covered |
| A.6.4 | Malformed NOMAD env var (non-int) is silently ignored | ✅ covered |
| A.6.5 | Params file nested YAML flattened to `--key=value` directives | **new** |
| A.6.6 | Params file takes lower priority than preamble | **new** |
| A.6.7 | Missing params file path → clear error | **new** |
| A.6.8 | Invalid YAML in params file → error | **new** |
| A.6.9 | Empty params file → no error, no changes to spec | **new** |

---

### A.7 Resource Parsing

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.7.1 | `--mem=2G` → 2048 MiB | ✅ covered |
| A.7.2 | `--mem=512M` → 512 MiB | ✅ covered |
| A.7.3 | `--mem=4096K` → 4 MiB | ✅ covered |
| A.7.4 | `--mem=1T` → 1 048 576 MiB | **new** |
| A.7.5 | `--mem=2g` (lowercase) → 2048 MiB | **new** |
| A.7.6 | `--time=01:30:00` → 5 400 seconds in timeout args | ✅ covered |
| A.7.7 | `--time=00:00:30` → 30 seconds | **new** |
| A.7.8 | No resources specified → resources block omitted entirely | ✅ covered |
| A.7.9 | `--gpus=0` → no devices block | **new** |

---

### A.8 Constraints and Affinities

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.8.1 | Single `constraint` + single `affinity` | ✅ covered |
| A.8.2 | Multiple constraints accumulate (both appear in HCL) | **new** |
| A.8.3 | Multiple affinities accumulate | **new** |
| A.8.4 | Constraint with `!=` operator | **new** |
| A.8.5 | Affinity default weight when not specified | **new** |
| A.8.6 | CLI `--constraint` flag overrides preamble (or adds to it) | **new** |

---

### A.9 Meta Block

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.9.1 | Multiple `--meta=key=value` pairs → meta block with all keys | ✅ covered |
| A.9.2 | `--meta=key=val=with=equals` → value contains = signs | **new** |
| A.9.3 | `abc_submission_time` is always present in meta | ✅ covered (ReschedulePreamble) |
| A.9.4 | Conda spec → `abc_conda` in meta | ✅ covered |
| A.9.5 | Pixi → `abc_pixi = "true"` in meta | **new** |
| A.9.6 | CLI `--meta` flag injects into meta block | **new** |

---

### A.10 Network and Ports

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.10.1 | `--port=mpi` → network block, NOMAD_IP_MPI, NOMAD_PORT_MPI, NOMAD_ADDR_MPI | ✅ covered |
| A.10.2 | `--no-network` → `mode = "none"` | ✅ covered |
| A.10.3 | Multiple `--port` labels → all appear in network block | **new** |

---

### A.11 Runtime Exposure Flags

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.11.1 | All exposure flags → NOMAD_ALLOC_ID, NOMAD_JOB_ID, etc. | ✅ covered |
| A.11.2 | Bare `--namespace` → NOMAD_NAMESPACE injected, no scheduler namespace set | ✅ covered |
| A.11.3 | `--namespace=hpc` → scheduler namespace set | ✅ covered |
| A.11.4 | Bare `--dc` → NOMAD_DC injected | ✅ covered |
| A.11.5 | `--dc=za-cpt-dc1` → datacenter in HCL job block | ✅ covered |

---

### A.12 Reschedule

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.12.1 | All reschedule directives → meta block has abc_reschedule_* keys | ✅ covered |
| A.12.2 | `--reschedule-mode=fail` → abc_reschedule_mode = "fail" | **new** |

---

### A.13 HPC Compatibility Env

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.13.1 | HPC compat disabled by default | ✅ covered |
| A.13.2 | `#ABC --hpc_compat_env` → SLURM_JOB_ID, PBS_JOBID, etc. | ✅ covered |
| A.13.3 | `--hpc-compat-env` CLI flag → same as preamble directive | ✅ covered |

---

### A.14 Config-Based Nomad Connection (offline, via ABC_CONFIG_FILE)

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.14.1 | Active context has `nomad_addr` → used as default (verifiable from flag echo) | **new** |
| A.14.2 | Active context has `nomad_token` → passed to client | **new** |
| A.14.3 | Config file missing → falls back to `http://127.0.0.1:4646` default | **new** |

---

### A.15 Error Cases

| # | Scenario | Key assertions |
|---|----------|----------------|
| A.15.1 | No script argument | ✅ covered |
| A.15.2 | Script file not found | ✅ covered |
| A.15.3 | `#ABC` directive without `--` prefix | ✅ covered |
| A.15.4 | Unknown `#ABC` directive | ✅ covered |
| A.15.5 | Non-integer `--nodes` | ✅ covered |
| A.15.6 | Invalid memory value | ✅ covered |
| A.15.7 | Invalid time format (not HH:MM:SS) | ✅ covered |
| A.15.8 | `--meta` without `=value` | ✅ covered |
| A.15.9 | Malformed NOMAD env var silently ignored | ✅ covered |
| A.15.10 | `#ABC --pixi=somevalue` → error (boolean flag takes no value) | **new** |
| A.15.11 | Non-integer `--gpus` | **new** |
| A.15.12 | Non-integer `--priority` | **new** |
| A.15.13 | Missing `--params-file` path | **new** |
| A.15.14 | Invalid YAML in `--params-file` | **new** |

---

## Section B — Integration Tests (`//go:build integration`)

Integration tests require:
- `NOMAD_ADDR` env var pointing to a live Nomad agent (e.g., `http://localhost:4646`)
- `NOMAD_TOKEN` env var with a valid ACL token (can be empty for dev agents with ACL disabled)
- The Nomad agent must be running in `batch` scheduling mode

Run with:
```bash
NOMAD_ADDR=http://localhost:4646 go test -tags integration -v ./cmd/job/...
```

### B.1 HCL Print Mode (no `--submit`)

Validates that the CLI can reach a live Nomad agent's HCL parse endpoint
(`/v1/jobs/parse`) without registering a job.

| # | Scenario | Key assertions |
|---|----------|----------------|
| B.1.1 | Valid HCL submitted to `/v1/jobs/parse` — Nomad returns parsed JSON | Live HCL round-trip |
| B.1.2 | Invalid HCL (manually corrupted) — Nomad parse returns 400, CLI shows error | Error propagation |

### B.2 Dry-run (`--dry-run`)

Calls `/v1/job/<id>/plan` on a live agent. Job is not registered.

| # | Scenario | Key assertions |
|---|----------|----------------|
| B.2.1 | `--dry-run` on a minimal exec job → plan output shown, job not in `abc job list` | Dry-run semantics |
| B.2.2 | `--dry-run` with insufficient resources → warnings in plan output | Resource constraint |

### B.3 Live Job Submission (`--submit`)

| # | Scenario | Key assertions |
|---|----------|----------------|
| B.3.1 | Submit minimal `exec` job (`sleep 1; exit 0`) → EvalID returned, job appears in list | Happy path |
| B.3.2 | Submit job → wait for completion → status = `complete` | Lifecycle |
| B.3.3 | Submit job that exits non-zero → status = `dead` after completion | Failure path |
| B.3.4 | Submit with explicit `--namespace=default` | Namespace routing |
| B.3.5 | Submit with `--nodes=2` → 2 allocations created | Parallel group |
| B.3.6 | Submit `docker` driver job (image: `busybox:latest`) | Docker driver |
| B.3.7 | Submit with GPU request on a node without GPU → plan shows failed alloc | GPU constraint miss |
| B.3.8 | Submit with `--name` override → job ID uses custom name | Job naming |

### B.4 Live Log Streaming (`--watch`)

| # | Scenario | Key assertions |
|---|----------|----------------|
| B.4.1 | `--submit --watch` on a job that prints to stdout → logs appear in terminal | Log streaming |
| B.4.2 | `--watch` streams stderr when job writes to stderr | Stderr streaming |
| B.4.3 | `--watch` terminates when job completes (no hang) | Proper EOF |

### B.5 Connection Error Handling

| # | Scenario | Key assertions |
|---|----------|----------------|
| B.5.1 | Bad `--nomad-addr` (port wrong) → clear connection error, not panic | Unreachable endpoint |
| B.5.2 | Bad token → Nomad returns 403 → CLI shows auth error | Auth failure |
| B.5.3 | Context cancellation (Ctrl-C) → CLI exits with code 130 | Interrupt handling |

### B.6 Task Driver Matrix

Tests each driver against a live Nomad agent that has the driver installed.

| # | Driver | Script | Assertion |
|---|--------|--------|-----------|
| B.6.1 | `exec` | `#!/bin/bash\necho hello` | alloc completes, stdout has "hello" |
| B.6.2 | `docker` | `#!/bin/bash\n#ABC --driver=docker\n#ABC --driver.config.image=busybox:latest\necho hello` | alloc completes |
| B.6.3 | `slurm` | `#!/bin/bash\n#SBATCH --job-name=live-slurm\necho done` | alloc completes (requires SLURM backend) |

---

## Coverage Summary

| Category | Existing | New (offline) | New (integration) | Total |
|----------|----------|---------------|-------------------|-------|
| exec driver | 8 | 8 | 3 | 19 |
| slurm driver | 5 | 4 | 1 | 10 |
| docker driver | 1 | 2 | 1 | 4 |
| hpc-bridge driver | 0 | 2 | 0 | 2 |
| Preamble parsing | 7 | 3 | 0 | 10 |
| Directive precedence | 6 | 5 | 0 | 11 |
| Resource parsing | 7 | 3 | 1 | 11 |
| Constraints / affinities | 1 | 5 | 0 | 6 |
| Meta block | 3 | 3 | 0 | 6 |
| Network / ports | 2 | 1 | 0 | 3 |
| Runtime exposure | 5 | 0 | 0 | 5 |
| Reschedule | 1 | 1 | 0 | 2 |
| HPC compat env | 3 | 0 | 0 | 3 |
| Config integration | 0 | 3 | 0 | 3 |
| Error cases | 10 | 5 | 2 | 17 |
| Live submission | 0 | 0 | 8 | 8 |
| Log streaming | 0 | 0 | 3 | 3 |
| Connection errors | 0 | 0 | 3 | 3 |
| **Total** | **59** | **54** | **22** | **135** |

---

## File Structure

```
cmd/job/
  run_test.go           — existing offline tests (51 tests)
  run_extra_test.go     — new offline tests (54 tests)  ← NEW
  run_integration_test.go — live Nomad tests (22 tests, build tag: integration)  ← NEW
  cmd_test.go           — command structure tests
  translate_test.go     — script translation tests
```

---

## Test Helpers

All offline test files use `writeTempScript(t, name, content) string` from `run_test.go`
and `executeCmd(t, args...) (string, error)` to drive the command.

Integration tests use an additional helper `requireNomad(t)` that calls `t.Skip()` when
`NOMAD_ADDR` is not set, making CI-without-Nomad safe even with the build tag.
