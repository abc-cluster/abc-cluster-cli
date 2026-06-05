package data

// presign.go — `abc data presign`
//
// Generates a presigned URL for any accessible S3 object by wrapping mcli.
//
//   mcli share download _abc/<bucket>/<key> --expire <duration>
//
// mcli outputs several lines; this command extracts the URL and prints it
// to stdout as a bare string — suitable for piping to curl or sharing with
// external collaborators who have no cluster credentials.
//
// Tool resolution: findTool("mcli") — ABC_BIN_MCLI → ~/.abc/binaries/mcli → PATH.

import (
	"bytes"
	"fmt"
	"os/exec"
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
curl or shared with collaborators who have no cluster credentials.

Uses mcli share download under the hood. Install mcli if not present:
  abc admin tools fetch mcli

Examples:

  # Generate a download link (default):
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/results.vcf

  # Short-lived link:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/sample.bam --expires 30m

  # 48-hour link for a collaborator:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/report.pdf --expires 48h

  # Download immediately with the generated URL:
  curl -L "$(abc data presign s3://bucket/key)" -o output.vcf

  # Generate an upload URL (PUT) — requires mcli share upload:
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

			url, err := presignViaMcli(cfg.ActiveCtx(), bucket, key, dur, method)
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), url)
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

// presignViaMcli uses mcli share download/upload to generate a presigned URL.
// mcli outputs multiple lines; this function extracts the URL line.
func presignViaMcli(ctx abccfg.Context, bucket, key string, expires time.Duration, method string) (string, error) {
	bin, alias, tmpDir, cleanup, err := mcliAlias(ctx)
	if err != nil {
		return "", fmt.Errorf("mcli alias: %w", err)
	}
	defer cleanup()

	// mcli share download/upload _abc/bucket/key --expire <duration>
	// duration format for mcli: mcli accepts "168h" style Go durations.
	durationStr := fmtMcliDuration(expires)

	mcliSubcmd := "download"
	if method == "PUT" {
		mcliSubcmd = "upload"
	}

	objectPath := alias + "/" + bucket
	if key != "" {
		objectPath += "/" + strings.TrimLeft(key, "/")
	}

	env := append(s3Env(ctx), "MCLI_CONFIG_DIR="+tmpDir)

	// Run mcli and capture output — we need to parse the URL from it.
	c := exec.Command(bin, "share", mcliSubcmd, objectPath, "--expire", durationStr)
	c.Env = env
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("mcli share %s: %w\n%s", mcliSubcmd, err, out.String())
	}

	url := extractURLFromMcliOutput(out.String())
	if url == "" {
		return "", fmt.Errorf("mcli share %s: could not find URL in output:\n%s", mcliSubcmd, out.String())
	}
	return url, nil
}

// extractURLFromMcliOutput scans mcli share output for the presigned URL.
// mcli prints several lines:
//
//	URL: http://host/bucket/key            ← plain URL, no signature
//	Expire: 30 minutes 0 seconds
//	Share: http://host/bucket/key?X-Amz-… ← presigned URL with signature
//
// We prefer the "Share:" line (which carries the signature query params) over
// the "URL:" line (which does not). Fall back to any http URL if Share is absent.
func extractURLFromMcliOutput(output string) string {
	var fallback string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		isShare := strings.HasPrefix(line, "Share:") || strings.HasPrefix(line, "Share :")
		isURLPrefix := strings.HasPrefix(line, "URL:") || strings.HasPrefix(line, "URL :")

		// Strip known prefixes.
		for _, prefix := range []string{"Share:", "Share :", "URL:", "URL :"} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimSpace(line[len(prefix):])
				break
			}
		}

		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			if isShare {
				return line // Prefer the Share line — it has the signature.
			}
			if isURLPrefix && fallback == "" {
				fallback = line
			}
			if !isShare && !isURLPrefix && fallback == "" {
				fallback = line
			}
		}
	}
	return fallback
}

// fmtMcliDuration converts a Go duration to a string mcli's --expire flag accepts.
// mcli accepts standard Go duration strings directly (e.g. "30m", "4h0m0s", "168h").
// We round to the nearest second and return the string representation.
func fmtMcliDuration(d time.Duration) string {
	// Round to seconds — mcli doesn't need sub-second precision.
	secs := int64(d.Seconds())
	rounded := time.Duration(secs) * time.Second
	return rounded.String()
}
