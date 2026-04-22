# Stress-ng workloads, Nomad containerd, and the abc job generator

This note records why we **stopped** trying to make the generic `abc job run` HCL generator automatically “fix” every OCI + Nomad containerd combination for templated `local/*.sh` scripts. Committed stress-ng and hyperfine workloads use **`community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8`** (shell + `stress-ng` + `hyperfine`, no runtime `apt`); only the **generator workarounds** were abandoned.

## What we wanted

- Run the same pattern as other workloads: Nomad **`template`** writes `local/<script>.sh`, task runs **`timeout … /bin/sh local/<script>.sh`** (or bash for non-containerd drivers).
- Use a real stress-ng image with a shell (not the scratch `ghcr.io/alexei-led/stress-ng` image, which has no `/bin/sh`).

## What went wrong (in order)

1. **Scratch stress-ng image**  
   `ghcr.io/alexei-led/stress-ng` is `FROM scratch`: only the static `stress-ng` binary. There is no shell, no `timeout` from coreutils, and no package manager. Templated shell scripts cannot run there without a different execution model (e.g. invoke `/stress-ng` with args only).

2. **Alpine + `apk`**  
   Switching to `docker.io/library/alpine:3.20` and `apk add stress-ng` avoids scratch, but batch tasks need outbound HTTPS to Alpine mirrors. Clusters that block or flake on `dl-cdn.alpinelinux.org` fail before stress-ng runs.

3. **Quay `container-perf-tools/stress-ng`**  
   A reasonable image with shell + `stress-ng`, but many such images set **`WORKDIR`** to something other than Nomad’s allocation task directory. The task then starts with cwd ≠ `$NOMAD_TASK_DIR`, so a **relative** path `local/script.sh` does not resolve to the templated file.

4. **`cd "${NOMAD_TASK_DIR}"` in `sh -c`**  
   Nomad substitutes `${…}` in some fields, but **`sh -c`** still needs **`NOMAD_TASK_DIR` in the process environment** for the shell to expand it. That is not guaranteed for every containerd setup; injecting `env { NOMAD_TASK_DIR = "…" }` helps only when the driver actually passes that env into the container the same way as the template mount.

5. **`${NOMAD_TASK_DIR}/local/…` in args**  
   On at least one cluster, the runtime value of `NOMAD_TASK_DIR` inside the container behaved like `/local` while templates were laid out such that paths looked like **`/local/local/…`**, i.e. **double `local`**. Heuristics to detect that are fragile and driver-specific.

6. **`nomad job` / containerd `config.work_dir`**  
   HashiCorp’s containerd task config rejected **`work_dir`** (“No argument or block type is named `work_dir`”), so we could not mirror Docker’s `work_dir` fix in the job spec.

7. **`env -C "${NOMAD_TASK_DIR}" …`**  
   GNU **`env -C`** sets the working directory without a shell wrapper and avoids relying on shell expansion. It works when **coreutils `env`** exists in the image and when Nomad interpolates `${NOMAD_TASK_DIR}` in **`args`** the way we expect. It still depends on the **same** path semantics as template placement, and it complicates every non-slurm job path (exec, docker, containerd) for a narrow case.

## Decision

- **Keep** workload scripts on the **Wave `hyperfine_stress-ng`** image above (same image for stress-ng and hyperfine examples; Grafana / integration comments track it).
- **Keep** `taskScriptShell`: **`containerd-driver` → `/bin/sh`**, other drivers → `/bin/bash` (scratch/OCI often have no bash). Hyperfine example scripts are **POSIX `sh`** (`set -eu`, no `pipefail`) so `timeout … /bin/sh local/…` matches how the generator invokes them.
- **Do not** encode further containerd cwd / `env -C` / `NOMAD_ALLOC_DIR` heuristics in **`internal/hclgen/job/generator.go`**. Those belong in **job-specific Nomad HCL**, a **custom driver config**, or **images whose `WORKDIR` matches how the operator mounts the alloc dir**.

## If you need this to work reliably today

- Prefer **`docker`** driver with an image whose **`WORKDIR`** is the task dir, or set Docker **`work_dir`** in hand-written Nomad HCL where supported.
- Or run stress-ng **without** a templated wrapper script (invoke the binary and args directly in `config`).
- Or maintain a **small custom image** built FROM UBI/Alpine with `WORKDIR` set to the path your Nomad + containerd stack uses for the allocation (the Seqera Wave image is one prebuilt option with both tools and a normal shell).

## Integration test

`TestIntegration_StressNgCPUWorkloadCompletes` is **opt-in**: set **`ABC_INTEGRATION_STRESS_NG=1`** to run it against a live cluster. Without it, the test is skipped so CI and laptops without the right containerd + network + `WORKDIR` combo are not red for reasons documented here.
