package data

// pipe.go — `abc data pipe`
// Reads stdin and uploads it to an S3 object via s5cmd pipe.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newPipeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "pipe <s3-uri>",
		Short:        "Upload stdin to an S3 object",
		SilenceUsage: true,
		Long: `Read from stdin and upload the bytes to the given S3 URI.

Enables capturing pipeline stdout directly into MinIO without a temporary
local file. Useful for streaming large outputs from Nextflow or custom scripts.

Examples:

  # Capture a command's output directly into MinIO:
  samtools view -bS input.sam | abc data pipe s3://su-mbhg-hostgen/user/calm-dassie/output.bam

  # Store a generated manifest:
  echo '{"version": 1}' | abc data pipe s3://su-mbhg-hostgen/user/calm-dassie/manifest.json

  # Compress on the fly before storing:
  cat large.vcf | gzip | abc data pipe s3://su-mbhg-hostgen/user/calm-dassie/large.vcf.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			bin, err := findTool("s5cmd")
			if err != nil {
				return err
			}
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "pipe", args), s3Env(cfg.ActiveCtx()))
		},
	}
	return cmd
}
