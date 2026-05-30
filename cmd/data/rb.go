package data

// rb.go — `abc data remove-bucket` (alias: rb)
// Removes an S3 bucket via s5cmd rb.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newRemoveBucketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "remove-bucket <s3-uri>",
		Aliases:      []string{"rb"},
		Short:        "Remove an S3 bucket",
		SilenceUsage: true,
		Long: `Remove a bucket from the cluster's MinIO storage.

The bucket must be empty unless --force is passed to s5cmd.

Examples:

  abc data remove-bucket s3://old-bucket
  abc data rb s3://old-bucket
  abc data rb s3://old-bucket --force`,
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
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "rb", args), s3Env(cfg.ActiveCtx()))
		},
	}
	return cmd
}
