package data

// synccmd.go — `abc data sync`
// Wraps s5cmd sync. All flags pass through verbatim.
// Named synccmd.go to avoid conflict with Go stdlib sync package.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var tool string

	cmd := &cobra.Command{
		Use:          "sync <src> <dst>",
		Short:        "Sync objects between two storage prefixes or local directories",
		SilenceUsage: true,
		Long: `Sync objects from source to destination using s5cmd sync.

src and dst accept s3:// URIs and local paths. All flags after the paths
are forwarded to s5cmd verbatim.

Examples:

  # Sync a local directory to storage:
  abc data sync ./results/ s3://su-mbhg-hostgen/user/calm-dassie/results/

  # Sync between two storage prefixes:
  abc data sync s3://su-mbhg-hostgen/user/calm-dassie/ s3://su-mbhg-hostgen/backup/

  # Sync and delete objects in dst not present in src:
  abc data sync s3://src/ s3://dst/ --delete

  # Dry-run to preview what would change:
  abc data sync s3://src/ s3://dst/ --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			bin, err := findTool(tool)
			if err != nil {
				return err
			}
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "sync", args), s3Env(cfg.ActiveCtx()))
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "s5cmd", "underlying tool: s5cmd (default) or rclone")
	return cmd
}
