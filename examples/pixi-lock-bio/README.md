# pixi-lock-bio — locked reproducible environments

Scripts in this directory use `--from=pixi.lock` which embeds **both**
`pixi.lock` and `pixi.toml` in the Nomad job at submit time and passes
`--locked` to `pixi install`. This guarantees bit-for-bit identical package
versions across every re-run.

## Generating the lock file

Run once locally (requires pixi):

```bash
cd examples/pixi-lock-bio
pixi install          # solves and writes pixi.lock
```

The resulting `pixi.lock` pins every transitive dependency to exact versions
and hashes. Commit both `pixi.toml` and `pixi.lock` to version control.

## Submitting

```bash
abc job run 01-trim-reads.sh \
  --meta sample=SRR123 \
  --meta r1=/data/raw/SRR123_R1.fastq.gz \
  --meta r2=/data/raw/SRR123_R2.fastq.gz \
  --meta outdir=/scratch/trimmed
```

The `#ABC --from=pixi.lock` preamble directive is picked up automatically —
no extra flags needed at submit time. The lock file and manifest are embedded
as Nomad templates; `pixi install --locked` runs inside the allocation before
the workload starts.

## Compared to pixi-bio

| | `pixi-bio` | `pixi-lock-bio` |
|---|---|---|
| `--from` | `pixi.toml` | `pixi.lock` |
| Install | latest matching | exact lock |
| Reproducibility | range | bit-for-bit |
| Extra file | — | `pixi.lock` (must be pre-generated) |
