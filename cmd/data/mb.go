package data

// mb.go — `abc data make-bucket` (alias: mb)
// Creates an S3 bucket via s5cmd mb.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newMakeBucketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "make-bucket <s3-uri>",
		Aliases:      []string{"mb"},
		Short:        "Create an S3 bucket",
		SilenceUsage: true,
		Long: `Create a new bucket on the cluster's MinIO storage.

Examples:

  abc data make-bucket s3://my-new-bucket
  abc data mb s3://my-new-bucket`,
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
			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "mb", args), s3Env(cfg.ActiveCtx()))
		},
	}
	return cmd
}
