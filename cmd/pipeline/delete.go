package pipeline

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a saved pipeline from the cluster",
		Long: `Delete a saved pipeline by name.

By default only the pipeline spec (stored in Nomad Variables) is removed.
Use --with-data to also delete associated MinIO data objects and
--with-jobs to stop and purge any running or completed Nomad jobs for this pipeline.`,
		Args: cobra.ExactArgs(1),
		RunE: runDelete,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("with-data", false, "Also delete associated data objects (MinIO/S3)")
	cmd.Flags().Bool("with-jobs", false, "Also stop and purge Nomad jobs for this pipeline")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	yes, _ := cmd.Flags().GetBool("yes")
	withData, _ := cmd.Flags().GetBool("with-data")
	withJobs, _ := cmd.Flags().GetBool("with-jobs")

	if !yes {
		extra := ""
		if withData {
			extra += " + data"
		}
		if withJobs {
			extra += " + jobs"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Delete saved pipeline %q%s? [y/N]: ", name, extra)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)
	ctx := cmd.Context()

	if err := deletePipeline(ctx, nc, name, ns); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline spec %q deleted.\n", name)

	// ── --with-jobs ──────────────────────────────────────────────────────────
	if withJobs {
		jobs, err := nc.ListJobs(ctx, name, ns)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: could not list jobs: %v\n", err)
		} else {
			purged := 0
			for _, j := range jobs {
				if _, err := nc.StopJob(ctx, j.ID, ns, true); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: purge job %q: %v\n", j.ID, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Purged job %q\n", j.ID)
				purged++
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  --with-jobs: %d job(s) purged.\n", purged)
		}
	}

	// ── --with-data ──────────────────────────────────────────────────────────
	if withData {
		cfg, err := abccfg.Load()
		if err != nil {
			return fmt.Errorf("load config for --with-data: %w", err)
		}
		s3, backendName, err := resolveDeleteS3Client(cfg)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: --with-data: %v\n", err)
		} else {
			totalDeleted := 0

			// Delete objects under common pipeline prefixes.
			// tusd stores uploads flat; pipelines may write to a directory by name.
			prefixes := []struct{ bucket, prefix string }{
				{"tusd", name + "/"},
				{name, ""},            // pipeline-named bucket (if it exists)
				{"nextflow", name + "/"}, // common nextflow output bucket prefix
			}
			for _, p := range prefixes {
				n, err := s3.DeleteObjectsWithPrefix(ctx, p.bucket, p.prefix)
				if err != nil {
					// Bucket may not exist — only warn.
					if !strings.Contains(err.Error(), "NoSuchBucket") && !strings.Contains(err.Error(), "404") {
						fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: delete %s/%s: %v\n", p.bucket, p.prefix, err)
					}
					continue
				}
				if n > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  Deleted %d object(s) from %s/%s%s\n", n, backendName, p.bucket, p.prefix)
					totalDeleted += n
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  --with-data: %d object(s) deleted from %s.\n", totalDeleted, backendName)
		}
	}

	return nil
}

// resolveDeleteS3Client builds a floor.S3Client from the active context config,
// reusing the same resolution logic as cmd/data.
func resolveDeleteS3Client(cfg *abccfg.Config) (*floor.S3Client, string, error) {
	ctx := cfg.ActiveCtx()

	type candidate struct {
		name      string
		serviceID string
	}
	for _, c := range []candidate{{"minio", "minio"}, {"rustfs", "rustfs"}} {
		endpoint, ok := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "endpoint")
		if !ok || endpoint == "" {
			endpoint, ok = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "http")
			if !ok || endpoint == "" {
				continue
			}
		}
		accessKey, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "access_key")
		if accessKey == "" {
			accessKey, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "user")
		}
		if accessKey == "" && ctx.Admin.ABCNodes != nil {
			accessKey = ctx.Admin.ABCNodes.S3AccessKey
			if accessKey == "" {
				accessKey = ctx.Admin.ABCNodes.MinioRootUser
			}
		}
		secretKey, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "secret_key")
		if secretKey == "" {
			secretKey, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "password")
		}
		if secretKey == "" && ctx.Admin.ABCNodes != nil {
			secretKey = ctx.Admin.ABCNodes.S3SecretKey
			if secretKey == "" {
				secretKey = ctx.Admin.ABCNodes.MinioRootPassword
			}
		}
		region, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "region")
		if region == "" && ctx.Admin.ABCNodes != nil {
			region = ctx.Admin.ABCNodes.S3Region
		}
		return floor.NewS3Client(endpoint, accessKey, secretKey, region), c.name, nil
	}
	return nil, "", fmt.Errorf(
		"no S3 endpoint configured for context %q\n"+
			"  Run: abc cluster capabilities sync\n"+
			"  Or:  abc config set admin.services.minio.endpoint http://<ip>:9000",
		cfg.ActiveContext,
	)
}
