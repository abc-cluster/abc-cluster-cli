# ABC vs PBS vs SLURM — Feature Parity & Migration Guide

This document maps PBS/Torque and SLURM features to their ABC equivalents,
identifies gaps, and provides the recommended migration pattern for each.

---

## Quick reference: directive mapping

| Feature | PBS directive | SLURM directive | ABC directive |
|---|---|---|---|
| Job name | `#PBS -N <name>` | `#SBATCH --job-name=<name>` | `#ABC --name=<name>` |
| CPU cores | `#PBS -l nodes=1:ppn=<n>` | `#SBATCH --cpus-per-task=<n>` | `#ABC --cores=<n>` |
| Memory | `#PBS -l mem=<n>gb` | `#SBATCH --mem=<n>G` | `#ABC --mem=<n>G` |
| Walltime | `#PBS -l walltime=HH:MM:SS` | `#SBATCH --time=HH:MM:SS` | `#ABC --time=HH:MM:SS` |
| Array / parallel | `#PBS -t 1-N` | `#SBATCH --array=1-N` | `#ABC --nodes=N` |
| GPU count | `#PBS -l ngpus=<n>` | `#SBATCH --gres=gpu:<n>` | `#ABC --gpus=<n>` |
| Queue / partition | `#PBS -q <queue>` | `#SBATCH --partition=<name>` | `#ABC --dc=<datacenter>` |
| Region | *(site-specific)* | `#SBATCH --clusters=<name>` | `#ABC --region=<region>` |
| Namespace | *(not supported)* | *(not supported)* | `#ABC --namespace=<ns>` |
| Job priority | *(site policy)* | `#SBATCH --priority=<n>` | `#ABC --priority=<n>` |
| Task driver | *(exec only)* | *(exec + container)* | `#ABC --driver=<driver>` |
| Job type | *(batch only)* | *(batch + interactive)* | *(batch only; `abc ssh` planned)* |
| Working dir | `#PBS -d <dir>` | *(submitting dir)* | `#ABC --chdir=<path>` |
| Dependency | `#PBS -W depend=afterok:ID` | `#SBATCH --dependency=afterok:ID` | `#ABC --depend=complete:job-id` |

---

## Runtime variable mapping
By default, ABC scripts should use `NOMAD_*` runtime variables. Legacy `SLURM_*` / `PBS_*` aliases are available only when compatibility mode is enabled with `#ABC --hpc_compat_env` (or CLI `--hpc-compat-env`).

| What | PBS variable | SLURM variable | ABC variable |
|---|---|---|---|
| Unique job/alloc ID | `$PBS_JOBID` | `$SLURM_JOB_ID` | `$NOMAD_ALLOC_ID` (`--alloc_id`) |
| Job name | `$PBS_JOBNAME` | `$SLURM_JOB_NAME` | `$NOMAD_JOB_NAME` (`--job_name`) |
| Array index | `$PBS_ARRAYID` *(1-based)* | `$SLURM_ARRAY_TASK_ID` *(1-based)* | `$NOMAD_ALLOC_INDEX` *(0-based)* |
| CPU count | `$PBS_NP` | `$SLURM_CPUS_PER_TASK` | `$NOMAD_CPU_CORES` (`--cpu_cores`) |
| Memory (MB) | *(not exposed)* | `$SLURM_MEM_PER_NODE` | `$NOMAD_MEMORY_LIMIT` (`--mem_limit`) |
| Work directory | `$PBS_O_WORKDIR` | `$SLURM_SUBMIT_DIR` | `$NOMAD_TASK_DIR` (`--task_dir`) |
| Shared dir | *(not standard)* | *(not standard)* | `$NOMAD_ALLOC_DIR` (`--alloc_dir`) |
| Secrets dir | *(not available)* | *(not available)* | `$NOMAD_SECRETS_DIR` (`--secrets_dir`) |
| Datacenter | *(not exposed)* | `$SLURM_CLUSTER_NAME` | `$NOMAD_DC` (`--dc`) |
| Namespace | *(not available)* | *(not available)* | `$NOMAD_NAMESPACE` (`--namespace`) |
| Region | *(not exposed)* | *(not exposed)* | `$NOMAD_REGION` *(Nomad scheduler region; injected at task runtime — not the same as `contexts.*.region` in `~/.abc/config.yaml`)* |
| Alloc name | *(not available)* | *(not available)* | `$NOMAD_ALLOC_NAME` (`--alloc_name`) |
| Parent job ID | *(not available)* | *(not available)* | `$NOMAD_JOB_PARENT_ID` (`--parent_job_id`) |

### ⚠ Array index offset

PBS and SLURM array indices are **1-based**. `NOMAD_ALLOC_INDEX` is **0-based**.

```bash
# PBS / SLURM — direct samplesheet lookup:
SAMPLE=$(sed -n "${PBS_ARRAYID}p" samplesheet.csv)
SAMPLE=$(sed -n "${SLURM_ARRAY_TASK_ID}p" samplesheet.csv)

# ABC — add 1 to convert 0-based to 1-based line number:
SAMPLE=$(sed -n "$((NOMAD_ALLOC_INDEX + 1))p" samplesheet.csv)
```

---

## Feature parity matrix

### ✅ Supported — full parity

| Feature | Notes |
|---|---|
| Single-task jobs | `--nodes=1` |
| Multi-core reservation | `--cores=N` |
| Memory reservation | `--mem=NM/G` |
| Walltime enforcement | `--time=HH:MM:SS` |
| Embarrassingly parallel array jobs | `--nodes=N` + `$NOMAD_ALLOC_INDEX` |
| GPU reservation | `--gpus=N` (NVIDIA via Nomad device plugin) |
| Job priority | `--priority=N` (1–100) |
| Datacenter targeting | `--dc=<datacenter>` (replaces queue/partition) |
| Region targeting | `--region=<region>` |
| Namespace isolation | `--namespace=<ns>` |
| Meta passthrough | `--meta=key=value` → `$NOMAD_META_<KEY>` |
| Job dependency | `--depend=complete:<job-id>` (prestart hook) |
| Job cancellation | `abc job stop <id> [--purge]` |
| Log streaming | `abc job logs <id> --follow` |
| Dry-run / plan | `abc job run <script> --dry-run` |
| Job status exit codes | `abc job status` exits 0/1/2 (scripting-friendly) |
| Parameterised dispatch | `abc job dispatch <id> --meta key=val` |
| Named ports (MPI) | `#ABC --port=<label>` |

### ⚠ Partial support — gap or workaround required

| Feature | PBS/SLURM | ABC | Gap / workaround |
|---|---|---|---|
| Max-concurrent array tasks | `#PBS -t 1-100%10` / `--array=1-100%10` | Not supported | Nomad schedules by resource availability; adjust `--nodes` or submit in batches |
| GPU type/model selection | `--gres=gpu:a100:2` | `--gpus=2` (count only) | Use `--dc=<gpu-dc>` to target GPU nodes; model selection via node constraints is planned |
| Job dependency by ID | `afterok:<JOBID>` | `--depend=complete:<job-name>` | ABC depends on job *name*, not numeric ID. Use unique names or polling |
| Per-task dependency | `aftercorr:$STEP1` (SLURM) | Not supported | Workaround: single-task step 2 polls `abc job status` |
| Log files on disk | `#PBS -o`, `#SBATCH --output` | No file output | Use `abc job logs <id> --type stdout > file.log` after the fact, or write to `$NOMAD_TASK_DIR` in-job |
| Interactive jobs | `qsub -I` / `salloc` | Planned (`abc ssh`) | Use `abc ssh <node>` once implemented |
| Job hold / release | `qhold` / `qrls` / `scontrol hold` | Not supported | Submit with low `--priority`, manually stop |

### ❌ Not supported — no equivalent

| Feature | PBS/SLURM | Notes |
|---|---|---|
| Email notifications | `#PBS -m abe` / `#SBATCH --mail-type` | Use `abc job status` in a cron/monitoring loop |
| Job requeue on node failure | `#SBATCH --requeue` | Nomad reschedules failed allocs if rescheduler policy is configured; not user-controlled per-job |
| Per-job accounting records | `tracejob` / `sacct` | `abc job show` shows alloc counts; per-job cost accounting is planned |
| Exclusive node allocation | `#PBS -l naccesspolicy=singlejob` / `#SBATCH --exclusive` | Not supported; Nomad is a bin-packing scheduler |
| Heterogeneous jobs | SLURM hetjob (`#SBATCH hetjob`) | Not supported; use multiple `abc job run` submissions |
| MPI process launch | `mpirun` via `$PBS_NODEFILE` / `srun` | `#ABC --port=<label>` injects network addresses; site-specific MPI integration via `hpc-bridge` driver |
| Fair-share / QOS | `#PBS -q <qos>` / `#SBATCH --qos` | Nomad priority is an integer; fair-share is not implemented |
| Job checkpointing | BLCR / DMTCP integration | Not supported |
| Preemption policies | `#SBATCH --preempt-mode` | Not supported |

---

## Migration patterns

### Pattern 1: Simple job

```bash
# PBS
#PBS -N my-job
#PBS -l nodes=1:ppn=8
#PBS -l mem=32gb
#PBS -l walltime=02:00:00

# SLURM
#SBATCH --job-name=my-job
#SBATCH --cpus-per-task=8
#SBATCH --mem=32G
#SBATCH --time=02:00:00

# ABC
#ABC --name=my-job
#ABC --cores=8
#ABC --mem=32G
#ABC --time=02:00:00
```

### Pattern 2: Array job (samplesheet-based)

```bash
# PBS: $PBS_ARRAYID  is 1-based
SAMPLE=$(sed -n "${PBS_ARRAYID}p" samplesheet.csv)

# SLURM: $SLURM_ARRAY_TASK_ID  is 1-based
SAMPLE=$(sed -n "${SLURM_ARRAY_TASK_ID}p" samplesheet.csv)

# ABC: $NOMAD_ALLOC_INDEX  is 0-based  → add 1 for sed
SAMPLE=$(sed -n "$((NOMAD_ALLOC_INDEX + 1))p" samplesheet.csv)
```

### Pattern 3: CPU count in tool flags

```bash
# PBS
bwa mem -t "$PBS_NP" ...

# SLURM
bwa mem -t "$SLURM_CPUS_PER_TASK" ...

# ABC  (#ABC --cpu_cores required)
bwa mem -t "$NOMAD_CPU_CORES" ...
```

### Pattern 4: Memory-aware heap sizing

```bash
# SLURM  (mem in MB already)
java -Xmx$((SLURM_MEM_PER_NODE - 2048))m -jar picard.jar ...

# ABC  (#ABC --mem_limit required, value is MB)
java -Xmx$((NOMAD_MEMORY_LIMIT - 2048))m -jar picard.jar ...
```

### Pattern 5: Output file routing

```bash
# PBS / SLURM — output goes to files automatically
#PBS -o results-${PBS_ARRAYID}.out
#SBATCH --output=results-%a.out

# ABC — output goes to Nomad log stream; retrieve with:
abc job logs <job-id> --alloc <prefix> --type stdout > results.log
# Or write files explicitly in the job body:
echo "result..." > "${NOMAD_TASK_DIR}/output/results.txt"
```

### Pattern 6: Job dependency chain

```bash
# PBS
STEP1=$(qsub job-step1.pbs)
qsub -W depend=afterok:$STEP1 job-step2.pbs

# SLURM
STEP1=$(sbatch --parsable job-step1.slurm)
sbatch --dependency=afterok:$STEP1 job-step2.slurm

# ABC — option A: inline --depend directive
#ABC --depend=complete:wgs-step1-align   # blocks on named job

# ABC — option B: shell polling (most reliable)
abc job run job-step1.abc.sh --submit
until abc job status wgs-step1-align; [ $? -eq 0 ]; do sleep 30; done
abc job run job-step2.abc.sh --submit
```

### Pattern 7: Managing log output

```bash
# PBS: logs written to <name>.o<ID>  and  <name>.e<ID>
# SLURM: logs written to <name>-<ID>.out

# ABC: stream stdout in real time
abc job logs my-job --follow

# ABC: fetch stderr after the fact
abc job logs my-job --type stderr

# ABC: fetch logs for a specific allocation
abc job logs my-job --alloc a1b2c3d4 --type stdout

# ABC: save logs to file
abc job logs my-job > job.log
```

### Workarounds for ABC preamble features

- constraints: use `#ABC --constraint=<attr><op><value>` or `#ABC --affinity=<attr><op><value>,weight=<n>` directly in script. For site-specific single-node behavior, use `#ABC --dc=<datacenter>`/`--region=<region>` and cluster-side job context.
- output/errors (stdout+stderr): use `#ABC --output=<name>` and `#ABC --error=<name>` in script; job generator now emits redirect into `${NOMAD_TASK_DIR}` from task shell running command.
- if you need explicit filesystem log file paths, in script set:
  - `echo ... >> "${NOMAD_TASK_DIR}/${ABC_OUTPUT:-job.out}"` and similarly for error using `2>>`.
- if ACL or token config is missing, use `--nomad-addr`, `--nomad-token`, `--region` from CLI or env to avoid `127.0.0.1:4646` local default.
- For preamble validation failure, use `abc job run --dry-run` first to inspect generated HCL and correct block structure.

---

## Command reference

| Action | PBS | SLURM | ABC |
|---|---|---|---|
| Submit | `qsub job.pbs` | `sbatch job.slurm` | `abc job run job.sh --submit` |
| Dry-run | `qsub -n job.pbs` *(limited)* | `sbatch --test-only` | `abc job run job.sh --dry-run` |
| Print HCL only | *(n/a)* | *(n/a)* | `abc job run job.sh` *(stdout)* |
| List jobs | `qstat` | `squeue` | `abc job list` |
| Job details | `qstat -f <ID>` | `scontrol show job <ID>` | `abc job show <id>` |
| Job status (scriptable) | `qstat -j <ID>; echo $?` | `squeue -j <ID>` | `abc job status <id>` *(exit 0/1/2)* |
| View logs | `cat <name>.o<ID>` | `cat <name>-<ID>.out` | `abc job logs <id> [--follow]` |
| Cancel / stop | `qdel <ID>` | `scancel <ID>` | `abc job stop <id>` |
| Remove job record | `qdel -W force <ID>` | `scancel --purge <ID>` | `abc job stop <id> --purge` |
| Dispatch parameterized | *(not standard)* | *(not standard)* | `abc job dispatch <id> --meta k=v` |
| SSH to node | `qsub -I` / ssh manually | `salloc` / `srun --pty` | `abc ssh <node>` *(planned)* |
