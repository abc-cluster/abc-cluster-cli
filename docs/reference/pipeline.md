---
sidebar_position: 6.7
---

# pipeline run

Submit a Nextflow pipeline as a head job to the ABC Nomad cluster, with optional [Wave](https://seqera.io/wave/) container builds via the Seqera Wave service.

## Basic usage

```bash
abc pipeline run <pipeline> [flags]
```

`<pipeline>` is a Nextflow pipeline source — a GitHub slug (`nextflow-io/hello`), a full repository URL, or a local path. The CLI generates a Nomad batch job that runs the Nextflow head process inside a Docker container; child jobs are dispatched to the cluster via the [nf-nomad](https://github.com/nextflow-io/nf-nomad) executor.

## Key flags

| Flag | Default | Description |
|---|---|---|
| `--revision` | *(default branch)* | Branch, tag, or commit SHA to check out |
| `--profile` | *(none)* | Nextflow config profile(s), comma-separated |
| `--params-file PATH` | *(none)* | YAML/JSON file with pipeline parameters |
| `--param key=value` | *(none)* | Inline parameter override (repeatable; merged on top of `--params-file`) |
| `--config PATH` | *(none)* | Extra `nextflow.config` to merge into the head job |
| `--work-dir PATH` | `/work/nextflow-work` | Shared work directory — local path or `s3://` URI |
| `--host-volume NAME` | `nextflow-work` | Nomad host volume for the work dir; use `-` to disable |
| `--nf-version TAG` | `25.10.4` | Nextflow Docker image tag |
| `--nf-plugin-version V` | `0.4.0-edge3` | nf-nomad plugin version |
| `--cpu MHz` | `1000` | Head job CPU allocation |
| `--memory MB` | `2048` | Head job memory allocation |
| `--datacenter LIST` | `dc1` | Nomad datacenter(s) |
| `--node HOSTNAME` | *(any)* | Pin head job to a specific Nomad node |
| `--name NAME` | `nextflow-head` | Override Nomad job name |
| `--resume` | `false` | Append `-resume` to the `nextflow run` command |
| `--session-id UUID` | *(none)* | Resume a specific Nextflow session (implies `--resume`) |
| `--wait` | `false` | Block until the head job completes |
| `--logs` | `false` | Stream head job logs after submit |
| `--timeout DURATION` | `2h` | Max wait time when using `--wait` |
| `--dry-run` | `false` | Print generated HCL without submitting |

## Wave container builds

Wave is a [Seqera](https://seqera.io/wave/) service that builds OCI containers on demand from conda/spack package specs or a custom Dockerfile. When Wave directives are present in `abc job run` scripts, abc submits a **Nomad prestart task** that calls the Wave CLI with `--await`, blocking until the container image is pullable before the main task starts.

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

The resolved image URI is deterministic — Wave computes it from a hash of the inputs — so it can be baked into the HCL before the build finishes. The prestart task ensures the image is pullable before the main task ever tries to pull it.

Wave CLI v1.8.2 is distributed through the cluster binary store (`abc-reserved/binary_tools/wave-linux-{amd64,arm64}`) and fetched automatically by the prestart task as a Nomad artifact — no manual installation on nodes required.

### `#ABC` Wave directives

Add any of the following directives to your script preamble. Wave activates automatically when at least one `wave-*` directive is present.

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

#### Directive reference

| Directive | Description |
|---|---|
| `--wave-conda-packages=pkg[=ver],...` | Conda packages to layer onto `--from`. Comma-separated, version optional. |
| `--wave-r-packages=pkg[=ver],...` | R packages from conda-forge (`r-ggplot2`, `r-dplyr`, …). Convenience alias — expands to `r-<pkg>` conda package names. |
| `--wave-spack-packages=pkg[@ver],...` | Spack packages. |
| `--wave-conda-file=PATH` | Path to a `conda env` YAML file. Content is embedded in the job — no shared filesystem access needed at runtime. |
| `--wave-spack-file=PATH` | Path to a `spack.yaml`. Embedded the same way. |
| `--wave-containerfile=PATH` | Path to a Dockerfile. Embedded and passed to Wave as a custom build context. |
| `--wave-platform=OS/ARCH` | Target platform (default: `linux/amd64`). |
| `--wave-token-secret=PATH:KEY` | Nomad Variable path and key holding the `TOWER_ACCESS_TOKEN` (default: `nomad/jobs:wave_token`). |

### Authentication

Wave requires a Seqera Platform token (`TOWER_ACCESS_TOKEN`). Store it in a Nomad Variable and reference it with `--wave-token-secret`:

```bash
# Store the token once
abc secrets set nomad/jobs wave_token=<your-seqera-token>

# Reference in your script (or rely on the default)
#ABC --wave-token-secret=nomad/jobs:wave_token
```

Anonymous Wave builds are rate-limited (25/day). An authenticated token raises this to 100 builds/hour.

### Examples

**Add conda packages to a base image:**

```bash
#!/bin/bash
#ABC --name=samtools-job
#ABC --driver=docker
#ABC --from=ubuntu:22.04
#ABC --wave-conda-packages=samtools=1.21,bwa=0.7.17

samtools view -c input.bam
```

**R analysis with conda-forge packages:**

```bash
#!/bin/bash
#ABC --name=r-analysis
#ABC --driver=docker
#ABC --from=ubuntu:22.04
#ABC --wave-r-packages=ggplot2=3.4.2,dplyr=1.1.4,tidyr

Rscript analysis.R
```

**Custom Dockerfile:**

```bash
#!/bin/bash
#ABC --name=custom-build
#ABC --driver=docker
#ABC --wave-containerfile=Dockerfile
#ABC --wave-platform=linux/amd64

python run_pipeline.py
```

**Conda environment file:**

```bash
#!/bin/bash
#ABC --name=conda-env-job
#ABC --driver=docker
#ABC --from=ubuntu:22.04
#ABC --wave-conda-file=environment.yml

python analysis.py
```

### Failure handling

If the Wave build fails, the prestart task exits non-zero and Nomad marks the allocation as `failed` before the main task starts. Build failure details are available in the prestart task logs:

```bash
abc job logs <job-id>          # streams both prestart and main task logs
abc job show <job-id>          # shows task-level status breakdown
```

Wave build status can also be checked on the [Seqera Platform](https://cloud.seqera.io) under your workspace's build history.

## Examples

```bash
# Run a public pipeline
abc pipeline run nextflow-io/hello --profile hello

# Pin to a release
abc pipeline run nf-core/rnaseq --revision 3.14.0 --profile test

# Pass parameters inline
abc pipeline run nf-core/sarek \
  --param genome=GRCh38 \
  --param input=samples.csv

# Use S3 work directory (no host volume needed)
abc pipeline run nextflow-io/hello \
  --work-dir s3://my-bucket/nextflow-work

# Resume a previous run
abc pipeline run nextflow-io/hello --resume

# Block and stream logs
abc pipeline run nf-core/rnaseq --wait --logs --timeout 4h

# Print HCL without submitting
abc pipeline run nextflow-io/hello --dry-run
```

## Saved pipelines

Frequently used pipeline configurations can be saved and recalled by name:

```bash
# Save a pipeline config
abc pipeline add rnaseq \
  --repo nf-core/rnaseq \
  --revision 3.14.0 \
  --profile test

# Run the saved pipeline (flags override saved values)
abc pipeline run rnaseq --param genome=GRCh38
```

Use `abc pipeline list` to see saved pipelines and `abc pipeline delete <name>` to remove one.

## See also

- [`abc module run`](./module.md) — run a single nf-core module via nf-pipeline-gen.
- [`abc job run`](./jobs.md) — run arbitrary shell scripts as Nomad batch jobs.
- [`abc admin tools`](./admin.md) — manage cluster tool binaries including the Wave CLI.
