package minio

import "github.com/spf13/cobra"

// NewCmd returns the "minio" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "minio",
		Short: "MinIO passthrough helpers",
		Long: `Commands for running MinIO client CLI operations.

  abc admin services minio cli ls local
  abc admin services minio cli alias list`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
