package data

// presign.go — `abc data presign`
//
// Generates a time-limited presigned URL for any accessible stored object via a
// direct SDK call (minio-go) — no external tool, no subprocess, no text parsing.
// The URL is printed to stdout as a bare string, suitable for piping to curl or
// sharing with collaborators who have no cluster credentials.
//
// Part of the SDK-for-control-plane plan:
// brainstorms/abc-data-platform/2026-06-05-sdk-vs-native-tools-for-data-ops.md

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newPresignCmd() *cobra.Command {
	var expires string
	var method string

	cmd := &cobra.Command{
		Use:          "presign <s3-uri>",
		Short:        "Generate an expiring URL for a stored object",
		SilenceUsage: true,
		Long: `Generate a time-limited expiring URL for any accessible stored object.

The URL is printed to stdout (bare, no decoration) so it can be piped to
curl or shared with collaborators who have no cluster credentials. Requires no
external tools — it is signed directly against the storage backend.

Examples:

  # Generate a download link (default):
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/results.vcf

  # Short-lived link:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/sample.bam --expires 30m

  # 48-hour link for a collaborator:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/report.pdf --expires 48h

  # Download immediately with the generated URL:
  curl -L "$(abc data presign s3://bucket/key)" -o output.vcf

  # Generate an upload URL (PUT):
  abc data presign s3://bucket/key --method PUT`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w (use Go duration: 30m, 4h, 48h)", expires, err)
			}
			if dur <= 0 {
				return fmt.Errorf("--expires must be positive")
			}
			if dur > 7*24*time.Hour {
				return fmt.Errorf("--expires cannot exceed 7 days (maximum for expiring URLs)")
			}

			method = strings.ToUpper(strings.TrimSpace(method))
			if method != "GET" && method != "PUT" {
				return fmt.Errorf("--method must be GET or PUT")
			}

			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}

			bucket, key, err := parseS3URI(args[0])
			if err != nil {
				return err
			}
			if key == "" {
				return fmt.Errorf("presign needs an object key: s3://%s/<key>", bucket)
			}

			signed, err := presignViaSDK(cmd.Context(), cfg.ActiveCtx(), bucket, key, dur, method)
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), signed)
			return nil
		},
	}

	cmd.Flags().StringVar(&expires, "expires", "4h", "URL expiry duration (e.g. 30m, 4h, 48h; max 7d)")
	cmd.Flags().StringVar(&method, "method", "GET", "HTTP method: GET (download) or PUT (upload)")
	return cmd
}

// parseS3URI splits an s3://bucket/key URI into (bucket, key).
func parseS3URI(uri string) (bucket, key string, err error) {
	lower := strings.ToLower(uri)
	if !strings.HasPrefix(lower, "s3://") {
		return "", "", fmt.Errorf("not an S3 URI (expected s3://bucket/key): %q", uri)
	}
	rest := uri[5:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return rest, "", nil
	}
	return rest[:slash], rest[slash+1:], nil
}

// presignViaSDK signs a GET (download) or PUT (upload) URL directly against the
// storage backend with minio-go — no external tool.
func presignViaSDK(ctx context.Context, actx abccfg.Context, bucket, key string, expires time.Duration, method string) (string, error) {
	cl, err := newMinioClient(actx)
	if err != nil {
		return "", err
	}
	var u *url.URL
	if method == "PUT" {
		u, err = cl.PresignedPutObject(ctx, bucket, key, expires)
	} else {
		u, err = cl.PresignedGetObject(ctx, bucket, key, expires, url.Values{})
	}
	if err != nil {
		return "", fmt.Errorf("presign %s s3://%s/%s: %w", method, bucket, key, err)
	}
	return u.String(), nil
}
