---
sidebar_position: 6.7
---

# pipeline run

Submit a Nextflow pipeline as a head job to the ABC Nomad cluster, with optional [Wave](https://seqera.io/wave/) container augmentation via the hybrid abc-wave / Seqera Wave router.

## Basic usage

```bash
abc pipeline run <pipeline> [flags]
```

`<pipeline>` is a Nextflow pipeline source — a GitHub slug (`nextflow-io/hello`), a full repository URL, or a local path. The CLI generates a Nomad batch job that runs the Nextflow head process inside a Docker container; child jobs are dispatched to the cluster via the [nf-nomad](https://github.com/nextflow-io/nf-nomad) executor.

## Key flags

### Pipeline source and runtime

| Flag | Default | Description |
|---|---|---|
| `--revision` | *(default branch)* | Branch, tag, or commit SHA to check out |
| `--profile` | *(none)* | Nextflow config profile(s), comma-separated |
| `--params-file PATH` | *(none)* | YAML/JSON file with pipeline parameters |
| `--param key=value` | *(none)* | Inline parameter override (repeatable; merged on top of `--params-file`) |
| `--config PATH` | *(none)* | Extra `nextflow.config` to merge into the head job |
| `--work-dir PATH` | `/work/nextflow-work` | Shared work directory — local path or `s3://` URI |
| `--host-volume NAME` | `nextflow-work` | Nomad host volume for the work dir; use `-` to disable |
| `--resume` | `false` | Append `-resume` to the `nextflow run` command |
| `--session-id UUID` | *(none)* | Resume a specific Nextflow session (implies `--resume`) |

### Container runtime

| Flag | Default | Description |
|---|---|---|
| `--singularity` | `false` | Use Singularity as the container runtime instead of Docker. With `--wave`, enables `ociAutoPull` so Singularity converts the Wave-augmented OCI image to SIF locally — no Wave-side SIF build required (compatible with Wave Lite). |
| `--apptainer` | `false` | Same as `--singularity` but emits `apptainer { }` config. Mutually exclusive with `--singularity`. |

### Wave container augmentation

| Flag | Default | Description |
|---|---|---|
| `--wave` | `false` | Enable Wave augmentation. Routes to abc-wave (if configured and healthy) or falls back to `wave.seqera.io`. |
| `--wave-endpoint URL` | *(router)* | Override the Wave endpoint directly (bypasses the abc-wave probe). Use `seqera` as shorthand for `https://wave.seqera.io`. |
| `--fusion` | `false` | Enable [Fusion v2](https://docs.seqera.io/fusion) filesystem alongside Wave. Always routes to `wave.seqera.io` (Fusion is not supported by Wave Lite). Requires an `s3://` work dir and a Seqera token in the Nomad Variable `nomad/jobs:wave_token`. Incompatible with `--singularity`/`--apptainer`. |

### Placement and resources

| Flag | Default | Description |
|---|---|---|
| `--node HOSTNAME` | *(any)* | Pin head job to a specific Nomad node |
| `--pin-workers` | `false` | Also pin every process to the same node as `--node` (single-host run) |
| `--worker-exclude-host HOSTNAME` | *(none)* | Force every process off this hostname (use with `--node` for a true head≠worker distributed test) |
| `--datacenter LIST` | `dc1` | Nomad datacenter(s) |
| `--nf-version TAG` | `25.10.4` | Nextflow Docker image tag |
| `--plugin id@version` | _(repeatable)_ | Pin a Nextflow plugin to a specific version. By default `nf-nomad` and `nf-nomad-s5cmd` resolve to the newest published release (bare `id "<name>"`); use this to pin one, e.g. `--plugin nf-nomad@0.4.0-edge8`. |
| `--cpu MHz` | `1000` | Head job CPU allocation |
| `--memory MB` | `2048` | Head job memory allocation |
| `--name NAME` | `nextflow-head` | Override Nomad job name |

### Behaviour

| Flag | Default | Description |
|---|---|---|
| `--wait` | `false` | Block until the head job completes |
| `--logs` | `false` | Stream head job logs after submit |
| `--timeout DURATION` | `2h` | Max wait time when using `--wait` |
| `--dry-run` | `false` | Print generated HCL without submitting |
| `--dev-plugins` | `false` | Load the nf-abc-cluster-dev plugin bundle into the head container |

---

## Wave container augmentation

There are two distinct Wave integration points in abc-cluster-cli:

| Integration | Trigger | Use case |
|---|---|---|
| **Pipeline Wave** (`--wave`) | `abc pipeline run` flag | Nextflow `wave { }` config — augments containers for every process at pull time. Runs on the cluster head job. |
| **Job Wave** (`#ABC wave-exec`) | `abc job run` directive | Pre-builds a conda container via the Wave CLI as a Nomad prestart task before the main task starts. |

These are independent. A pipeline run can use `--wave` without any job-level wave directives, and vice versa.

### Hybrid routing

The CLI uses a hybrid router that automatically selects the best available Wave endpoint:

```
--wave (pipeline augmentation)
  │
  ├── probe admin.services.wave.http  (3 s timeout, cached 5 min)
  │     │
  │     ├── healthy  →  abc-wave  (local, low-latency augmentation)
  │     └── absent / unhealthy  →  wave.seqera.io  (public fallback)
  │
  └── --fusion flag  →  always wave.seqera.io  (Fusion not supported by Wave Lite)

#ABC wave-exec / wave-conda (job builds)
  └── always wave.seqera.io  (Wave Lite has no build service)
```

Configure the abc-wave URL in your context:

```bash
abc config set admin.services.wave.http https://wave.seedling.abc-cluster.cloud
```

When set, every `--wave` pipeline run will probe this URL first. If abc-wave is unreachable the fallback is transparent — the pipeline still runs, just augmented by Seqera Wave instead.

### Pipeline augmentation (`--wave`)

Emits a `wave { enabled = true; endpoint = "..." }` block in the generated `nextflow.headjob.config`. Nextflow sends an augmentation request to Wave for each process container before pulling it; Wave returns a `targetImage` URL pointing to the augmented manifest through Wave's OCI proxy.

```bash
# Route automatically (abc-wave if healthy, else Seqera)
abc pipeline run nf-core/rnaseq --wave --revision 3.14.0

# Force Seqera Wave explicitly
abc pipeline run nf-core/rnaseq --wave-endpoint seqera

# Pin to abc-wave unconditionally
abc pipeline run nf-core/rnaseq --wave-endpoint https://wave.seedling.abc-cluster.cloud
```

### Container runtime compatibility

Wave's augmentation protocol is OCI-based. The `targetImage` URL returned by Wave is an OCI image reference that any OCI-capable runtime can pull.

| Runtime | Wave Lite support | Notes |
|---|---|---|
| Docker | ✅ Full | Default; `docker { enabled = true }` |
| containerd | ✅ Full | Pulls OCI manifest from Wave proxy natively |
| Podman | ✅ Full | OCI-native |
| Singularity / Apptainer | ✅ via OCI pull | Use `--singularity`/`--apptainer` — see below |
| Singularity SIF (Wave-built) | ❌ Wave Lite only | Requires `freeze` / build service; use full Seqera Wave |

### Singularity and Apptainer

Wave Lite cannot build SIF files (the build service is disabled). The workaround is to let the local Singularity/Apptainer runtime pull Wave's augmented OCI image and convert it to SIF on the worker node via `ociAutoPull`:

```bash
# Singularity: augment with abc-wave, convert OCI → SIF locally on each worker
abc pipeline run nf-core/rnaseq --wave --singularity

# Apptainer equivalent
abc pipeline run nf-core/rnaseq --wave --apptainer

# Without --wave, emits singularity { enabled = true } only (no ociAutoPull)
abc pipeline run nf-core/rnaseq --singularity
```

Generated nextflow config with `--wave --singularity`:

```groovy
singularity {
  enabled     = true
  ociAutoPull = true   // only emitted when --wave is also set
}

wave {
  enabled  = true
  endpoint = "https://wave.seedling.abc-cluster.cloud"
}
```

`ociAutoPull` requires Nextflow ≥ 23.10. For older versions, pass the Wave `targetImage` URL directly via `--config` with a custom `singularity.pullTimeout` override.

### Fusion filesystem

[Fusion v2](https://docs.seqera.io/fusion) mounts S3 buckets as a POSIX filesystem inside process containers, eliminating the need for a shared NFS/host volume.

**Requirements:**
- `--wave` or `--wave-endpoint seqera` (Fusion always routes to Seqera Wave — Wave Lite does not support it)
- An `s3://` work directory (`--work-dir s3://bucket/path`)
- A Seqera Platform token stored in the Nomad Variable `nomad/jobs:wave_token`
- Docker or containerd runtime (Fusion is FUSE-based; incompatible with `--singularity`/`--apptainer`)

```bash
# Store your Seqera token once
abc admin services nomad cli -- var put nomad/jobs wave_token=<your-seqera-token>

# Run with Fusion
abc pipeline run nf-core/rnaseq \
  --wave --fusion \
  --work-dir s3://my-bucket/nextflow-work \
  --revision 3.14.0
```

The CLI injects `TOWER_ACCESS_TOKEN` and `SEQERA_ACCESS_TOKEN` into the head job automatically when `--fusion` is set, reading from `nomad/jobs:wave_token` (override with `--wave-endpoint` if your token lives elsewhere). The `{{ if }}` guard in the template prevents an empty token from triggering a 401 from Wave.

Generated nextflow config with `--wave --fusion`:

```groovy
wave {
  enabled  = true
  endpoint = "https://wave.seqera.io"
}

fusion {
  enabled = true
}
```

---

## Job-level Wave builds (`abc job run`)

Wave is also used for individual Nomad batch jobs submitted with `abc job run`. This is a separate, build-oriented integration: the Wave CLI builds a container from a conda spec before the main task starts. It always uses `wave.seqera.io` (Wave Lite has no build service).

### How it works

```
abc job run script.sh
  ↓ (wave directives detected)
  CLI calls: wave [args] -o json      ← resolves targetImage URI instantly
  ↓
  Nomad job submitted with:
    prestart task  →  wave [args] --await   (blocks on cluster until built)
    main task      →  runs with the resolved Wave image
```

### `#ABC` Wave directives

```bash
#!/bin/bash
#ABC --from=ubuntu:22.04                        # base image to augment
#ABC --wave-conda-packages=samtools=1.21,bwa    # conda packages to add
#ABC --wave-r-packages=ggplot2=3.4.2,dplyr      # R packages (conda-forge r-*)
#ABC --wave-spack-packages=samtools@1.21        # spack packages
#ABC --wave-conda-file=environment.yml          # conda env file (embedded)
#ABC --wave-spack-file=spack.yaml               # spack file (embedded)
#ABC --wave-containerfile=Dockerfile            # custom Dockerfile (embedded)
#ABC --wave-platform=linux/amd64               # target platform (default)
#ABC --wave-token-secret=nomad/jobs:wave_token  # Nomad Variable for TOWER_ACCESS_TOKEN
```

| Directive | Description |
|---|---|
| `--wave-conda-packages=pkg[=ver],...` | Conda packages to layer onto `--from`. Comma-separated, version optional. |
| `--wave-r-packages=pkg[=ver],...` | R packages from conda-forge (`r-ggplot2`, `r-dplyr`, …). Expands to `r-<pkg>` conda package names. |
| `--wave-spack-packages=pkg[@ver],...` | Spack packages. |
| `--wave-conda-file=PATH` | Path to a `conda env` YAML file. Content is embedded in the job. |
| `--wave-spack-file=PATH` | Path to a `spack.yaml`. Embedded the same way. |
| `--wave-containerfile=PATH` | Path to a Dockerfile. Embedded and passed to Wave as a custom build context. |
| `--wave-platform=OS/ARCH` | Target platform (default: `linux/amd64`). |
| `--wave-token-secret=PATH:KEY` | Nomad Variable path and key for `TOWER_ACCESS_TOKEN` (default: `nomad/jobs:wave_token`). |

### Authentication

Job-level Wave builds always target `wave.seqera.io` and require a Seqera Platform token:

```bash
# Store the token once (also used by --fusion in pipeline runs)
abc admin services nomad cli -- var put nomad/jobs wave_token=<your-seqera-token>
```

Anonymous builds are rate-limited (25/day). An authenticated token raises this to 100 builds/hour.

---

## Examples

```bash
# Run a public pipeline
abc pipeline run nextflow-io/hello --profile hello

# Pin to a release
abc pipeline run nf-core/rnaseq --revision 3.14.0 --profile test

# Wave augmentation (routes to abc-wave if healthy, else Seqera)
abc pipeline run nf-core/rnaseq --wave --revision 3.14.0

# Wave + Fusion on S3 (always Seqera Wave)
abc pipeline run nf-core/rnaseq \
  --wave --fusion \
  --work-dir s3://my-bucket/nextflow-work

# Wave + Singularity (OCI pull → local SIF conversion, Wave Lite compatible)
abc pipeline run nf-core/rnaseq --wave --singularity

# Pass parameters inline
abc pipeline run nf-core/sarek \
  --param genome=GRCh38 \
  --param input=samples.csv

# Use S3 work directory (no host volume needed)
abc pipeline run nextflow-io/hello \
  --work-dir s3://my-bucket/nextflow-work

# Pin head to a node, exclude it from workers (distributed test)
abc pipeline run nextflow-io/hello \
  --node nomad02 \
  --worker-exclude-host nomad02

# Resume a previous run
abc pipeline run nextflow-io/hello --resume

# Block and stream logs
abc pipeline run nf-core/rnaseq --wait --logs --timeout 4h

# Print HCL without submitting
abc pipeline run nextflow-io/hello --dry-run
```

## Saved pipelines

Frequently used pipeline configurations can be saved and recalled by name. Wave and runtime fields (`waveEndpoint`, `fusionEnabled`, `containerRuntime`) are serialised as part of the saved spec.

```bash
# Save a pipeline config with Wave enabled
abc pipeline add rnaseq \
  --repo nf-core/rnaseq \
  --revision 3.14.0 \
  --profile test

# Run the saved pipeline (flags override saved values)
abc pipeline run rnaseq --param genome=GRCh38 --wave
```

Use `abc pipeline list` to see saved pipelines and `abc pipeline delete <name>` to remove one.

## See also

- [`abc module run`](./module.md) — run a single nf-core module via nf-pipeline-gen.
- [`abc job run`](./jobs.md) — run arbitrary shell scripts as Nomad batch jobs, including job-level Wave builds.
- [`abc admin tools`](./admin.md) — manage cluster tool binaries including the Wave CLI.
