package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// parseBucketPath normalises an `abc data ls`/`stat` argument into
// (bucket, prefix-or-key). It accepts:
//
//	bucket                  → ("bucket", "")
//	bucket/                 → ("bucket", "")
//	bucket/some/prefix      → ("bucket", "some/prefix")
//	s3://bucket             → ("bucket", "")
//	s3://bucket/some/prefix → ("bucket", "some/prefix")
//
// The s3:// scheme is accepted because the rest of the CLI emits
// s3:// URIs (workdir, results, upload destination paths in
// `abc pipeline run` output) — users naturally paste those into
// `abc data ls`. A naive strings.Cut on the first `/` would split
// inside the `//` in `s3://` and produce a `s3:` bucket name (which
// MinIO rejects with InvalidBucketName — the symptom that surfaced
// this bug during the 2026-05-27 hostgen demo dress rehearsal).
func parseBucketPath(arg string) (bucket, rest string) {
	arg = strings.TrimPrefix(arg, "s3://")
	bucket, rest, _ = strings.Cut(arg, "/")
	return bucket, rest
}

func newLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list [bucket[/prefix]]",
		Aliases: []string{"ls"},
		Short:   "List buckets or objects in cluster storage",
		Long: `List objects in cluster storage.

Without arguments: list all buckets.
With a bucket name: list objects at the bucket root.
With a bucket/prefix: list objects under that prefix.

  abc data ls
  abc data ls su-mbhg-hostgen
  abc data ls su-mbhg-hostgen/user/calm-dassie/

Credentials and endpoint are resolved from the active context
(configured automatically by 'abc cluster capabilities sync').`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLs,
	}
	cmd.Flags().String("storage", "", "Storage backend override (default: auto-detected)")
	cmd.Flags().Int("max", 1000, "Maximum number of objects to list")
	cmd.Flags().Bool("long", false, "Long format: show size and last-modified")
	return cmd
}

func newStatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stat <bucket/key>",
		Short: "Show metadata for a stored object",
		Long: `Print size, ETag, last-modified date, and user metadata for a single object.

  abc data stat su-mbhg-hostgen/user/calm-dassie/genome.fa`,
		Args: cobra.ExactArgs(1),
		RunE: runStat,
	}
	cmd.Flags().String("storage", "", "Storage backend override (default: auto-detected)")
	return cmd
}

func runLs(cmd *cobra.Command, args []string) error {
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	maxKeys, _ := cmd.Flags().GetInt("max")
	long, _ := cmd.Flags().GetBool("long")

	cl, err := newMinioClient(cfg.ActiveCtx())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// No argument: list buckets.
	if len(args) == 0 {
		buckets, err := cl.ListBuckets(cmd.Context())
		if err != nil {
			return fmt.Errorf("list buckets: %w", err)
		}
		if len(buckets) == 0 {
			fmt.Fprintln(out, "  No buckets found.")
			return nil
		}
		fmt.Fprintf(out, "  Buckets:\n\n")
		for _, b := range buckets {
			if long {
				fmt.Fprintf(out, "  %-30s  %s\n", b.Name, b.CreationDate.Format("2006-01-02 15:04"))
			} else {
				fmt.Fprintf(out, "  %s\n", b.Name)
			}
		}
		return nil
	}

	bucket, prefix := parseBucketPath(args[0])

	dirs, objs, err := listOneLevel(cmd.Context(), cl, bucket, prefix, maxKeys)
	if err != nil {
		return err
	}

	// Smart trailing slash: when the user wrote "users" (no slash) and the
	// only result is a DIR matching "users/" with no objects, they almost
	// certainly meant "users/" — auto-recurse one step into it. A small
	// stderr note ensures the expansion isn't silent.
	if !strings.HasSuffix(prefix, "/") && len(objs) == 0 && len(dirs) == 1 &&
		dirs[0] == prefix+"/" {
		expanded := prefix + "/"
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  [hint] %q is a folder — listing %q (trailing '/' added)\n",
			prefix, expanded)
		prefix = expanded
		dirs, objs, err = listOneLevel(cmd.Context(), cl, bucket, prefix, maxKeys)
		if err != nil {
			return err
		}
	}

	if len(objs) == 0 && len(dirs) == 0 {
		fmt.Fprintf(out, "  No objects found at %s/%s\n", bucket, prefix)
		return nil
	}

	fmt.Fprintf(out, "  %s/%s\n\n", bucket, prefix)
	for _, d := range dirs {
		fmt.Fprintf(out, "  DIR  %s\n", d)
	}
	if long && len(objs) > 0 {
		fmt.Fprintf(out, "  %-12s  %-20s  %s\n", "SIZE", "LAST MODIFIED", "KEY")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 72))
	}
	for _, o := range objs {
		if long {
			fmt.Fprintf(out, "  %-12s  %-20s  %s\n", formatSize(o.size), o.mod.Format("2006-01-02 15:04:05"), o.key)
		} else {
			fmt.Fprintf(out, "  %s\n", o.key)
		}
	}
	fmt.Fprintf(out, "\n  %d object(s)", len(objs))
	if maxKeys > 0 && len(objs) == maxKeys {
		fmt.Fprintf(out, " (truncated at %d; use --max to increase)", maxKeys)
	}
	fmt.Fprintln(out)
	return nil
}

// listObjRow is the per-object row used by listOneLevel.
type listObjRow struct {
	key  string
	size int64
	mod  time.Time
}

// listOneLevel runs a non-recursive (delimiter='/') ListObjects for
// bucket/prefix, splitting common-prefix "DIR" entries from object entries.
// Stops after maxKeys objects when maxKeys > 0.
func listOneLevel(ctx context.Context, cl *minio.Client, bucket, prefix string, maxKeys int) (dirs []string, objs []listObjRow, err error) {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for o := range cl.ListObjects(lctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: false}) {
		if o.Err != nil {
			return nil, nil, fmt.Errorf("list %s/%s: %w", bucket, prefix, o.Err)
		}
		if strings.HasSuffix(o.Key, "/") { // common prefix
			dirs = append(dirs, o.Key)
			continue
		}
		objs = append(objs, listObjRow{o.Key, o.Size, o.LastModified})
		if maxKeys > 0 && len(objs) >= maxKeys {
			break
		}
	}
	return dirs, objs, nil
}

func runStat(cmd *cobra.Command, args []string) error {
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cl, err := newMinioClient(cfg.ActiveCtx())
	if err != nil {
		return err
	}

	bucket, key := parseBucketPath(args[0])
	if bucket == "" || key == "" {
		return fmt.Errorf("specify <bucket>/<key>, e.g. su-mbhg-hostgen/user/calm-dassie/genome.fa")
	}

	info, err := cl.StatObject(cmd.Context(), bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("stat s3://%s/%s: %w", bucket, key, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  s3://%s/%s\n\n", bucket, key)
	fmt.Fprintf(out, "  %-14s %s\n", "Size", formatSize(info.Size))
	fmt.Fprintf(out, "  %-14s %s\n", "Modified", info.LastModified.Format(time.RFC3339))
	if info.ContentType != "" {
		fmt.Fprintf(out, "  %-14s %s\n", "Content-Type", info.ContentType)
	}
	fmt.Fprintf(out, "  %-14s %s\n", "ETag", strings.Trim(info.ETag, "\""))
	if info.VersionID != "" {
		fmt.Fprintf(out, "  %-14s %s\n", "Version", info.VersionID)
	}
	if info.StorageClass != "" {
		fmt.Fprintf(out, "  %-14s %s\n", "Storage-Class", info.StorageClass)
	}
	if len(info.UserMetadata) > 0 {
		fmt.Fprintln(out, "\n  User metadata:")
		keys := make([]string, 0, len(info.UserMetadata))
		for k := range info.UserMetadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "    %-12s %s\n", k, info.UserMetadata[k])
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
