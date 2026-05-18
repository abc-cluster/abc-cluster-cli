---
sidebar_position: 6.5
---

# module run

Generate a Nextflow driver pipeline for an nf-core module (via [nf-pipeline-gen](https://github.com/abc-cluster/nf-pipeline-gen)) and submit it to Nomad as a batch job.

## Basic usage

```bash
abc module run <module-name> [flags]
```

`<module-name>` is the full nf-core module path including the org prefix, e.g. `nf-core/fastqc` or `nf-core/samtools/sort`. The CLI submits a two-task Nomad batch job:

1. **`generate` (prestart)** — fetches `pipeline-gen.jar` + the nf-core/modules tarball, optionally validates a user-supplied samplesheet, then generates the driver pipeline under the shared host volume.
2. **`nextflow` (main)** — runs `nextflow run main.nf` against the generated driver.

## Key flags

| Flag | Description |
|---|---|
| `--samplesheet PATH` | Local CSV samplesheet (see [Samplesheets](#samplesheets) below). Validated locally then again cluster-side before driver generation. |
| `--params-file PATH` | Optional `params.yml` to vendor into the driver (default: auto-generated from the module's `meta.yml`). |
| `--config-file PATH` | Optional `module.config` to vendor into the driver (default: empty). |
| `--profile NAME` | Nextflow profile (default: `test`). Auto-includes `test` when `--test` is set. |
| `--test` | Run the module's bundled `tests/main.nf.test` fixtures, staged from `nf-core/test-datasets`. |
| `--datacenter LIST` | Nomad datacenter(s). Default `dc1`; pass `'*'` for any-datacenter placement. |
| `--driver NAME` | Nomad task driver (default: `docker`; `containerd-driver` on containerd-enabled nodes). |
| `--host-volume NAME` | Shared work-dir host volume (default: `scratch`). |
| `--wait` / `--logs` | Block on completion / stream logs (auto-on in interactive shells). |
| `--dry-run` | Print Nomad HCL without submitting. |

### JAR resolution

The prestart task fetches `pipeline-gen.jar` via three resolution paths, tried in order:

1. **`--pipeline-gen-jar-url URL`** — full URL to the JAR (no version-path concatenation, no `sha256sums.txt` lookup at the same prefix).
2. **`--pipeline-gen-url-base BASE` + `--pipeline-gen-version V`** — versioned mirror layout `<base>/<version>/pipeline-gen.jar` with sha256 verification.
3. **GitHub releases API** — falls through here when neither flag is set; needs `GITHUB_TOKEN` (or `GH_TOKEN`).

If you have run `abc admin tools push nf-pipeline-gen` against the active context, the CLI **auto-resolves path 1** from the resulting artifact URL — no flag needed.

### Active-context fallbacks

`module run` resolves the Nomad address, token, and namespace from the active `~/.abc/config.yaml` context when not passed explicitly:

- `--nomad-addr` / `--nomad-token` ← `admin.services.nomad.{nomad_addr,nomad_token}`
- `--namespace` ← `admin.abc_nodes.nomad_namespace` (so jobs land in the namespace where your nodes actually live, not `default`)

## Samplesheets

The `--samplesheet PATH` flag wires a local CSV through the prestart task for validation against the module's `meta.yml` *before* driver generation. Three stages:

1. **Local pre-flight** (instant, no Nomad round-trip) — file exists, non-empty, RFC-4180 parseable, has a header row.
2. **Base64 ship** — the CSV travels via the `ABC_SAMPLESHEET_B64` env var; same pattern as `--params-file` / `--config-file`. No S3 upload, no separate artifact.
3. **Cluster-side validate** (authoritative) — the prestart runs `pipeline-gen --validate-samplesheet` against the module's `meta.yml`. Exit 0 → driver gen + Nextflow proceed. Exit 1 → the alloc fails fast with the validator's full issue list in stderr.

### Scaffold a starter sheet — `module samplesheet emit`

To get a working CSV with the right column shape for a module, submit a one-shot emit job:

```bash
abc module samplesheet emit nf-core/<module> [--output PATH]
```

The CLI submits a small Nomad batch task that runs `pipeline-gen module --emit-samplesheet`, captures the resulting CSV into a Nomad Variable (`nomad/jobs/<jobid>/samplesheet/result`), then downloads it to `--output PATH` (default `./samplesheet-<module-slug>.csv`).

Headers come labelled from the module's `meta.yml` file inputs — no guessing what each column is for:

```text
nf-core/seqkit/stats           sample,reads
nf-core/samtools/sort          sample,bam,fasta,fai
nf-core/bcftools/view          sample,vcf,index
nf-core/picard/markduplicates  sample,reads
nf-core/bedtools/intersect     sample,intervals1,intervals2
nf-core/gatk4/markduplicates   sample,bam
nf-core/plink/recode           sample,bed
```

The single seeded data row uses the file references from the module's bundled `tests/main.nf.test` — a working example you can edit row-by-row to point at your real data.

### Diagnostic round-trip on emit failures

If the emit task fails (jar not found, module slug typo, etc.), the CLI surfaces the cluster-side log without needing alloc-log read perms. The emit task always publishes the Nomad Variable on exit, including a `diag` item carrying the last 16 KB of the task log. The CLI prints it under `--- emit task log (last 16 KB) ---`:

```text
$ abc module samplesheet emit nf-core/seqkit/stats
  Submitting samplesheet emit job ss-emit-nf-core-seqkit-stats-1d9be1c6 ...
  Job        ss-emit-nf-core-seqkit-stats-1d9be1c6
  ...
--- emit task log (last 16 KB) ---
emit task starting (module=nf-core/seqkit/stats version=latest)
Error parsing options:
Unknown option: "--emit-samplesheet"
...
--- end ---
emit job ss-emit-nf-core-seqkit-stats-1d9be1c6 ended with status "failed"
```

### End-to-end happy path

```bash
# 1. Scaffold the CSV from the module's bundled tests.
abc module samplesheet emit nf-core/plink/extract
# → ./samplesheet-nf-core-plink-extract.csv (sample,bed,bim,fam,variants)

# 2. Edit the CSV in place — swap the example URLs for your data, add rows.
$EDITOR ./samplesheet-nf-core-plink-extract.csv

# 3. Submit the run. The prestart re-validates against meta.yml.
abc module run nf-core/plink/extract \
  --samplesheet ./samplesheet-nf-core-plink-extract.csv
```

Validation failure example (sheet has 1 file column, plink/extract expects 4):

```text
Pre-flight (local): /tmp/wrong-shape.csv exists, 1 data row(s), 2 column(s). OK
  Module run submitted
  Job        m-plink-extract-…
prestart stderr:
  Samplesheet validation failed; aborting before driver generation.
prestart stdout (validator output):
  file columns: 1 (meta.yml expects 4: bed, bim, fam, variants)
  FAILED:
    - file column count 1 does not match meta.yml file inputs (4: bed, bim, fam, variants)
```

The job fails before driver generation runs and before Nextflow ever starts — saves a multi-minute round-trip on every typo.

## Examples

```bash
# Run with bundled test fixtures (--test is shorthand for the test profile + dataset staging)
abc module run nf-core/fastqc --test

# Run with your own params + config
abc module run nf-core/samtools/sort \
  --params-file params.yml \
  --config-file module.config

# Run with a samplesheet (validated cluster-side before driver gen)
abc module run nf-core/plink/extract --samplesheet ./samples.csv

# Use a JAR mirror (skip GitHub) — usually auto-resolved from `abc admin tools push`
abc module run nf-core/fastp \
  --pipeline-gen-jar-url https://s3.seedling.abc-cluster.cloud/abc-reserved/binary_tools/nf-pipeline-gen-any

# Print the HCL without submitting
abc module run nf-core/fastqc --dry-run
```

## See also

- `abc admin tools push nf-pipeline-gen` — pushes a locally-built JAR to the cluster mirror; the artifact URL is then auto-resolved by every subsequent `module run` / `module samplesheet emit`.
- [`abc job run`](./jobs.md) — for ad-hoc shell jobs without a generated driver.
