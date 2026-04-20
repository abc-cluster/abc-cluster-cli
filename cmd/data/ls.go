package data

import (
	"fmt"
	"strings"
	"time"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls [bucket[/prefix]]",
		Short: "List S3 buckets or objects (MinIO / RustFS)",
		Long: `List objects stored in the abc-nodes S3-compatible store.

Without arguments: list all buckets.
With a bucket name: list objects at the bucket root.
With a bucket/prefix: list objects under that prefix.

  abc data ls
  abc data ls tusd
  abc data ls tusd/uploads/

Credentials and endpoint are resolved from the active context
(admin.services.minio or admin.services.rustfs after 'abc cluster capabilities sync').`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLs,
	}
	cmd.Flags().String("storage", "", "Storage backend: minio or rustfs (default: whichever is configured)")
	cmd.Flags().Int("max", 1000, "Maximum number of objects to list")
	cmd.Flags().Bool("long", false, "Long format: show size and last-modified")
	return cmd
}

func newStatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stat <bucket/key>",
		Short: "Show metadata for an S3 object (MinIO / RustFS)",
		Long: `Print size, ETag, last-modified date, and user metadata for a single object.

  abc data stat tusd/my-upload-id`,
		Args: cobra.ExactArgs(1),
		RunE: runStat,
	}
	cmd.Flags().String("storage", "", "Storage backend: minio or rustfs (default: whichever is configured)")
	return cmd
}

// resolveS3Client builds an S3Client from the active context config.
// It prefers the backend named by --storage; otherwise picks the first configured one.
func resolveS3Client(cfg *abccfg.Config, backend string) (*floor.S3Client, string, error) {
	ctx := cfg.ActiveCtx()

	type candidate struct {
		name      string
		serviceID string // key in admin.services.*
	}
	order := []candidate{
		{"minio", "minio"},
		{"rustfs", "rustfs"},
	}
	if backend != "" {
		order = []candidate{{backend, backend}}
	}

	for _, c := range order {
		endpoint, ok := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "endpoint")
		if !ok || endpoint == "" {
			// Fall back to "http" for services that use that field.
			endpoint, ok = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "http")
			if !ok || endpoint == "" {
				continue
			}
		}
		accessKey, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "access_key")
		secretKey, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "secret_key")

		// Fall back to user/password (MinIO root credentials stored by config sync).
		if accessKey == "" {
			accessKey, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "user")
		}
		if secretKey == "" {
			secretKey, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, c.serviceID, "password")
		}

		// Fall back to abc_nodes static credentials.
		if accessKey == "" && ctx.Admin.ABCNodes != nil {
			accessKey = ctx.Admin.ABCNodes.S3AccessKey
			if accessKey == "" {
				accessKey = ctx.Admin.ABCNodes.MinioRootUser
			}
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

func runLs(cmd *cobra.Command, args []string) error {
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	backend, _ := cmd.Flags().GetString("storage")
	maxKeys, _ := cmd.Flags().GetInt("max")
	long, _ := cmd.Flags().GetBool("long")

	s3, backendName, err := resolveS3Client(cfg, backend)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// No argument: list buckets.
	if len(args) == 0 {
		buckets, err := s3.ListBuckets(cmd.Context())
		if err != nil {
			return fmt.Errorf("list buckets (%s): %w", backendName, err)
		}
		if len(buckets) == 0 {
			fmt.Fprintf(out, "  No buckets found on %s.\n", backendName)
			return nil
		}
		fmt.Fprintf(out, "  Buckets on %s:\n\n", backendName)
		for _, b := range buckets {
			if long {
				fmt.Fprintf(out, "  %-30s  %s\n", b.Name, b.CreationDate.Format("2006-01-02 15:04"))
			} else {
				fmt.Fprintf(out, "  %s\n", b.Name)
			}
		}
		return nil
	}

	// Parse "bucket" or "bucket/prefix".
	path := args[0]
	bucket, prefix, _ := strings.Cut(path, "/")

	objects, commonPrefixes, err := s3.ListObjects(cmd.Context(), bucket, prefix, maxKeys)
	if err != nil {
		return fmt.Errorf("list objects (%s/%s): %w", backendName, bucket, err)
	}

	if len(objects) == 0 && len(commonPrefixes) == 0 {
		fmt.Fprintf(out, "  No objects found at %s/%s\n", bucket, prefix)
		return nil
	}

	fmt.Fprintf(out, "  %s/%s\n\n", bucket, prefix)

	// Print common prefixes (subdirectories) first.
	for _, cp := range commonPrefixes {
		fmt.Fprintf(out, "  DIR  %s\n", cp)
	}

	if long {
		fmt.Fprintf(out, "  %-12s  %-20s  %s\n", "SIZE", "LAST MODIFIED", "KEY")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 72))
	}
	for _, o := range objects {
		key := o.Key
		if long {
			fmt.Fprintf(out, "  %-12s  %-20s  %s\n",
				formatSize(o.Size),
				o.LastModified.Format("2006-01-02 15:04:05"),
				key,
			)
		} else {
			fmt.Fprintf(out, "  %s\n", key)
		}
	}
	fmt.Fprintf(out, "\n  %d object(s)", len(objects))
	if maxKeys > 0 && len(objects) == maxKeys {
		fmt.Fprintf(out, " (truncated at %d; use --max to increase)", maxKeys)
	}
	fmt.Fprintln(out)
	return nil
}

func runStat(cmd *cobra.Command, args []string) error {
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	backend, _ := cmd.Flags().GetString("storage")

	s3, backendName, err := resolveS3Client(cfg, backend)
	if err != nil {
		return err
	}

	path := args[0]
	bucket, key, ok := strings.Cut(path, "/")
	if !ok || key == "" {
		return fmt.Errorf("specify <bucket>/<key>, e.g. tusd/my-upload-id")
	}

	obj, meta, err := s3.HeadObject(cmd.Context(), bucket, key)
	if err != nil {
		return fmt.Errorf("stat (%s/%s/%s): %w", backendName, bucket, key, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  Object: %s/%s\n\n", bucket, key)
	fmt.Fprintf(out, "  %-16s %s\n", "Backend:", backendName)
	fmt.Fprintf(out, "  %-16s %s\n", "Size:", formatSize(obj.Size))
	fmt.Fprintf(out, "  %-16s %s\n", "Last Modified:", obj.LastModified.Format(time.RFC3339))
	fmt.Fprintf(out, "  %-16s %s\n", "ETag:", obj.ETag)
	if obj.StorageClass != "" {
		fmt.Fprintf(out, "  %-16s %s\n", "Storage Class:", obj.StorageClass)
	}
	if len(meta) > 0 {
		fmt.Fprintf(out, "\n  User Metadata:\n")
		for k, v := range meta {
			fmt.Fprintf(out, "  %-16s %s\n", k+":", v)
		}
	}
	fmt.Fprintln(out)
	return nil
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
