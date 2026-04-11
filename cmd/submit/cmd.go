// Package submit implements the "abc submit" porcelain command.
// It auto-detects whether <target> is a Nextflow pipeline, an nf-core module,
// or a local batch script (optionally with a conda wrapper) and dispatches to
// the appropriate underlying HCL generator and Nomad submit path.
package submit

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewSubmitCmd returns the "submit" top-level command.
func NewSubmitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit <target>",
		Short: "Auto-detect and submit a pipeline, module, or batch script to Nomad",
		Long: `Submit a pipeline, nf-core module, or batch script to the ABC Nomad cluster.

<target> can be:
  - A saved pipeline name             e.g.  rnaseq
  - A Nextflow repository path        e.g.  nf-core/rnaseq
  - A full GitHub/GitLab URL          e.g.  https://github.com/nf-core/rnaseq
  - An nf-core module path            e.g.  nf-core/modules/bwa/mem
  - A local batch script              e.g.  ./bwa-align.sh
  - A conda tool name (with --conda)  e.g.  fastqc  --conda fastqc
  - A pixi task (with --pixi)         e.g.  fastqc  --pixi

Detection order (first match wins):
  1. --type pipeline|job|module  — forced
  2. --conda <spec> / --pixi     — job with generated conda or pixi wrapper
  3. local file exists           — job
  4. starts with http(s)://      — pipeline
  5. ≥ 3 path segments           — module
  6. exactly one "/"             — pipeline (owner/repo)
  7. Nomad Variables lookup      — pipeline (saved name)
  8. error: use --type to disambiguate

EXAMPLES

  # Run a public Nextflow pipeline
  abc submit nf-core/rnaseq --input samplesheet.csv --output /results

  # Run a saved pipeline by name
  abc submit rnaseq --param genome=GRCh38

  # Run an nf-core module
  abc submit nf-core/modules/bwa/mem --input reads.fastq.gz --output /results

  # Run a local batch script
  abc submit ./align.sh --wait --logs

  # Run a conda tool without writing a script
  abc submit fastqc --conda fastqc --input sample.fastq.gz --output /qc

  # Use mamba as the solver instead of conda
  abc submit fastqc --conda fastqc --conda-solver mamba --input sample.fastq.gz

  # Run a pixi task
  abc submit fastqc --pixi --input sample.fastq.gz --output /qc

  # Dry-run: print generated HCL without submitting
  abc submit nf-core/rnaseq --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: runSubmit,
	}

	// Data / params
	cmd.Flags().String("input", "", "Input file/directory (→ params.input)")
	cmd.Flags().String("output", "", "Output directory (→ params.outdir, nf-core convention)")
	cmd.Flags().StringArray("param", nil, "Extra param in key=value format (repeatable)")

	// Mode
	cmd.Flags().String("type", "", "Force dispatch mode: pipeline, job, or module")

	// Pipeline flags
	cmd.Flags().String("revision", "", "Git branch/tag/SHA (pipeline mode)")
	cmd.Flags().String("profile", "", "Nextflow profile(s), comma-separated")
	cmd.Flags().String("config", "", "Extra Nextflow config file (pipeline mode)")
	cmd.Flags().String("work-dir", "", "Nextflow work directory")
	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag")

	// Conda / job flags
	cmd.Flags().String("conda", "", "Conda package spec — triggers conda wrapper mode")
	cmd.Flags().String("conda-solver", "conda", "Conda solver executable: conda, mamba, or micromamba")
	cmd.Flags().Bool("pixi", false, "Run <target> via pixi run (triggers pixi wrapper mode)")
	cmd.Flags().Int("cores", 0, "CPU cores (job/conda/pixi mode)")
	cmd.Flags().String("mem", "", "Memory, e.g. 4G, 512M (job/conda/pixi mode)")
	cmd.Flags().String("time", "", "Walltime HH:MM:SS (job/conda/pixi mode)")
	cmd.Flags().StringArray("tool-arg", nil, "Extra arg appended to tool invocation (repeatable, conda/pixi modes)")

	// Shared
	cmd.Flags().String("name", "", "Override Nomad job name")
	cmd.Flags().String("namespace", utils.EnvOrDefault("ABC_NAMESPACE", "NOMAD_NAMESPACE"),
		"Nomad namespace (or set ABC_NAMESPACE/NOMAD_NAMESPACE)")
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s)")
	cmd.Flags().Bool("wait", false, "Block until job completes")
	cmd.Flags().Bool("logs", false, "Stream logs after submit")
	cmd.Flags().Bool("dry-run", false, "Print generated HCL without submitting")

	// Nomad connection (also readable from root persistent flags)
	cmd.Flags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.Flags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")

	return cmd
}
