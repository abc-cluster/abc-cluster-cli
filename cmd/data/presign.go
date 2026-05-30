package data

// presign.go — `abc data presign`
//
// Generates a presigned URL for any S3 object the active context can access.
// Uses hand-rolled AWS Sig V4 query presign — no minio-go or aws-sdk-go dependency.
//
// Output is a bare URL on stdout, suitable for piping to curl or sharing
// with external collaborators who have no cluster credentials.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newPresignCmd() *cobra.Command {
	var expires string
	var method string

	cmd := &cobra.Command{
		Use:          "presign <s3-uri>",
		Short:        "Generate a presigned URL for an S3 object",
		SilenceUsage: true,
		Long: `Generate a time-limited presigned URL for any accessible S3 object.

The URL is printed to stdout (bare, no decoration) so it can be piped to
curl or shared with collaborators who have no cluster credentials.

Credentials never appear in the URL — the URL is signed with HMAC-SHA256
and expires after --expires (default: 4h).

Examples:

  # Generate a download link (default):
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/results.vcf

  # Short-lived link for a large BAM:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/sample.bam --expires 30m

  # 48-hour link for a collaborator:
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/report.pdf --expires 48h

  # Download immediately with the generated URL:
  curl -L "$(abc data presign s3://bucket/key)" -o output.vcf

  # Generate an upload URL (PUT):
  abc data presign s3://su-mbhg-hostgen/user/calm-dassie/upload-here.fa --method PUT`,
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
				return fmt.Errorf("--expires cannot exceed 7 days (MinIO maximum for presigned URLs)")
			}

			method = strings.ToUpper(strings.TrimSpace(method))
			if method != "GET" && method != "PUT" {
				return fmt.Errorf("--method must be GET or PUT")
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := cfg.ActiveCtx()

			endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(ctx)
			if err != nil {
				return fmt.Errorf("resolve credentials: %w", err)
			}

			// Parse the s3:// URI into bucket and key.
			bucket, key, err := parseS3URI(args[0])
			if err != nil {
				return err
			}

			signed, err := presignV4(endpoint, accessKey, secretKey, "us-east-1", bucket, key, method, dur)
			if err != nil {
				return fmt.Errorf("sign URL: %w", err)
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
	rest := uri[5:] // strip "s3://"
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return rest, "", nil // bucket-only URI
	}
	return rest[:slash], rest[slash+1:], nil
}

// presignV4 generates an AWS Sig V4 presigned URL for an S3 object.
// No external dependencies — uses only stdlib crypto/hmac, crypto/sha256.
func presignV4(endpoint, accessKey, secretKey, region, bucket, key, method string, expires time.Duration) (string, error) {
	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	datetimeStr := now.Format("20060102T150405Z")

	ep := strings.TrimRight(endpoint, "/")
	parsedEP, err := url.Parse(ep)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", ep, err)
	}
	host := parsedEP.Host

	// Object path — always absolute.
	objectPath := "/" + bucket
	if key != "" {
		objectPath += "/" + strings.TrimLeft(key, "/")
	}

	// Credential scope.
	scope := dateStr + "/" + region + "/s3/aws4_request"

	// Canonical query string — must be sorted by key name.
	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+scope)
	q.Set("X-Amz-Date", datetimeStr)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	canonicalQuery := q.Encode() // url.Values.Encode() sorts keys

	// Canonical request.
	canonicalHeaders := "host:" + host + "\n"
	signedHeaders := "host"
	payloadHash := "UNSIGNED-PAYLOAD"
	canonicalRequest := strings.Join([]string{
		method,
		objectPath,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign.
	reqHash := sigV4SHA256Hex(canonicalRequest)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		datetimeStr,
		scope,
		reqHash,
	}, "\n")

	// Signing key: HMAC chain — date → region → service → "aws4_request".
	signingKey := hmacSHA256Bytes(
		hmacSHA256Bytes(
			hmacSHA256Bytes(
				hmacSHA256Bytes([]byte("AWS4"+secretKey), dateStr),
				region,
			),
			"s3",
		),
		"aws4_request",
	)

	// Final signature.
	sig := hex.EncodeToString(hmacSHA256Bytes(signingKey, stringToSign))

	return ep + objectPath + "?" + canonicalQuery + "&X-Amz-Signature=" + sig, nil
}

func sigV4SHA256Hex(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func hmacSHA256Bytes(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}
