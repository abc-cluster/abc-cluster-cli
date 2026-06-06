package data

// du.go — `abc data disk-usage` (alias: du). Sums object sizes via a direct SDK
// call (minio-go ListObjects) — no external tool.

import (
	"context"
	"fmt"

	minio "github.com/minio/minio-go/v7"
	"github.com/spf13/cobra"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
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
			ctx := cfg.ActiveCtx()

			// Default to the active user's prefix when no arg given.
			if len(args) == 0 {
				ns := ctx.AbcNodesNomadNamespaceOrDefault()
				user := storageUserSlug(ctx)
				args = []string{"s3://" + ns + "/user/" + user + "/"}
			}
			bucket, prefix := parseBucketPath(args[0])

			cl, err := newMinioClient(ctx)
			if err != nil {
				return err
			}

			lctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			var total, count int64
			for o := range cl.ListObjects(lctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
				if o.Err != nil {
					return fmt.Errorf("disk-usage s3://%s/%s: %w", bucket, prefix, o.Err)
				}
				total += o.Size
				count++
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "  %-9s %s (%d bytes)\n", "Total:", formatSize(total), total)
			fmt.Fprintf(out, "  %-9s %d\n", "Objects:", count)
			fmt.Fprintf(out, "  %-9s s3://%s/%s\n", "Prefix:", bucket, prefix)
			return nil
		},
	}
	return cmd
}
