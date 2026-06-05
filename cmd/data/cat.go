package data

// cat.go — `abc data cat`
// Streams an S3 object to stdout via s5cmd cat.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cat <s3-uri>",
		Short:        "Stream a stored object to stdout",
		SilenceUsage: true,
		Long: `Print the contents of a stored object to stdout without saving a local file.

Useful for inspecting headers, manifests, or small text files without
downloading them. Pipe the output through head, grep, jq, etc.

Examples:

  # Print a VCF header:
  abc data cat s3://su-mbhg-hostgen/user/calm-dassie/results.vcf | head -50

  # Inspect a JSON manifest:
  abc data cat s3://su-mbhg-hostgen/user/calm-dassie/manifest.json | jq .

  # Count lines in a large file without downloading it:
  abc data cat s3://su-mbhg-hostgen/user/calm-dassie/data.csv | wc -l`,
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
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "cat", args), s3Env(cfg.ActiveCtx()))
		},
	}
	return cmd
}
