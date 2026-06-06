package data

// s3sdk.go — direct SDK (minio-go) access to the storage backend for
// control-plane operations (presign, stat, list, du, …). Data MOVEMENT stays on
// the native tools (s5cmd/aria2c/rclone); this is for metadata/query calls where
// a subprocess + binary dependency + text parsing is the wrong tool.
//
// Plan: brainstorms/abc-data-platform/2026-06-05-sdk-vs-native-tools-for-data-ops.md

import (
	"fmt"
	"net/url"
	"strings"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// newMinioClient builds a minio-go client for the active context, using the same
// credsource-aware credential resolution as the native-tool path
// (resolveS3Creds → handles the seedling/v1 broker exchange as well as local
// creds), so the SDK and the tools always agree on identity.
func newMinioClient(ctx abccfg.Context) (*minio.Client, error) {
	endpoint, accessKey, secretKey := resolveS3Creds(ctx)
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("no storage endpoint configured for the active context\n  Run: abc cluster capabilities sync")
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, fmt.Errorf("no storage credentials available for the active context\n  Run: abc cluster capabilities sync (or re-claim your slot)")
	}

	host, secure := splitEndpoint(endpoint)
	cl, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: resolveS3Region(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("build storage client: %w", err)
	}
	return cl, nil
}

// splitEndpoint turns an endpoint (with or without scheme) into the host:port +
// a secure (TLS) flag that minio.New expects.
func splitEndpoint(endpoint string) (host string, secure bool) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			return u.Host, u.Scheme == "https"
		}
	}
	// Bare host[:port] — default to TLS unless it's an obvious plain-HTTP form.
	secure = true
	if strings.HasPrefix(endpoint, "localhost") || strings.HasPrefix(endpoint, "127.0.0.1") {
		secure = false
	}
	return endpoint, secure
}

// resolveS3Region mirrors resolveS3Client's region resolution.
func resolveS3Region(ctx abccfg.Context) string {
	if r, ok := abccfg.GetAdminFloorField(&ctx.Admin.Services, "minio", "region"); ok && r != "" {
		return r
	}
	if r, ok := abccfg.GetAdminFloorField(&ctx.Admin.Services, "rustfs", "region"); ok && r != "" {
		return r
	}
	if ctx.Admin.ABCNodes != nil && ctx.Admin.ABCNodes.S3Region != "" {
		return ctx.Admin.ABCNodes.S3Region
	}
	return "us-east-1"
}
