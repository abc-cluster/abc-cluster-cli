package data

// rm.go — `abc data remove` (aliases: rm, delete)
// Wraps s5cmd rm. All flags pass through verbatim.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	var tool string

	cmd := &cobra.Command{
		Use:     "remove <s3-uri>...",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove one or more S3 objects",
		Long: `Remove objects from MinIO/S3 storage.

Wraps s5cmd rm. All flags after the S3 URI(s) are forwarded verbatim.

Examples:

  # Remove a single object:
  abc data remove s3://su-mbhg-hostgen/user/calm-dassie/old.vcf

  # Remove all objects under a prefix:
  abc data remove s3://su-mbhg-hostgen/user/calm-dassie/tmp/* --verbose

  # Aliases:
  abc data rm s3://bucket/key
  abc data delete s3://bucket/key`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			bin, err := findTool(tool)
			if err != nil {
				return err
			}
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "rm", args), s3Env(cfg.ActiveCtx()))
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "s5cmd", "underlying tool: s5cmd (default)")
	return cmd
}
