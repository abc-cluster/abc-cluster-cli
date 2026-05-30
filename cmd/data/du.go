package data

// du.go — `abc data disk-usage` (alias: du)
// Reports storage usage for a prefix via s5cmd du.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newDiskUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "disk-usage [s3-uri]",
		Aliases:      []string{"du"},
		Short:        "Show storage usage for a bucket or prefix",
		SilenceUsage: true,
		Long: `Report total object count and bytes for a bucket or prefix.

Without arguments: reports usage for the active user's default prefix.
With an s3:// URI: reports usage for that bucket or prefix.

Examples:

  # Show usage for your default prefix:
  abc data disk-usage

  # Show usage for a specific prefix:
  abc data disk-usage s3://su-mbhg-hostgen/user/calm-dassie/

  # Short alias:
  abc data du s3://su-mbhg-hostgen/`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			bin, err := findTool("s5cmd")
			if err != nil {
				return err
			}
			// Default to the active user's prefix when no arg given.
			if len(args) == 0 {
				ctx := cfg.ActiveCtx()
				ns := ctx.AbcNodesNomadNamespaceOrDefault()
				user := storageUserSlug(ctx)
				args = []string{"s3://" + ns + "/user/" + user + "/"}
			}
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "du", args), s3Env(cfg.ActiveCtx()))
		},
	}
	return cmd
}
