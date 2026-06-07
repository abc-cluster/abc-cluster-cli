package appgen

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/credsource"
)

// MinIOCreds is the resolved storage endpoint + admin credentials for the
// active context. The credentials are the context's MinIO identity (root/admin
// on seedling PoC) used to create per-app service accounts and to check bucket
// existence.
type MinIOCreds struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	Secure    bool   // TLS — derived from the endpoint scheme
	Host      string // host[:port], scheme stripped (for minio-go / madmin-go)
}

// ResolveMinIOCreds reads the storage endpoint + credentials from the active
// context, honouring the credsource broker (seedling/v1) the same way
// cmd/data's resolveS3Creds does. It returns a clear error when nothing is
// configured so deploy fails before any side effects.
func ResolveMinIOCreds(ctx config.Context) (*MinIOCreds, error) {
	endpoint, ak, sk := resolveStorageCreds(ctx)
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("no storage endpoint configured for the active context\n  Run: abc cluster capabilities sync")
	}
	if strings.TrimSpace(ak) == "" || strings.TrimSpace(sk) == "" {
		return nil, fmt.Errorf("no storage credentials available for the active context\n  Run: abc cluster capabilities sync (or re-claim your slot)")
	}
	host, secure := splitEndpoint(endpoint)
	return &MinIOCreds{
		Endpoint:  endpoint,
		AccessKey: ak,
		SecretKey: sk,
		Region:    resolveRegion(ctx),
		Secure:    secure,
		Host:      host,
	}, nil
}

// resolveStorageCreds mirrors cmd/data.resolveS3Creds (unexported there): broker
// first when cred_source is a broker tier, then admin.services.minio/rustfs,
// then legacy abc_nodes fields.
func resolveStorageCreds(ctx config.Context) (endpoint, accessKey, secretKey string) {
	if credsource.IsBroker(ctx.CredSource) {
		if creds, err := credsource.ResolveFromContext(context.Background(), ctx); err == nil {
			ep := strings.TrimRight(strings.TrimSpace(creds.Minio.Endpoint), "/")
			ak := strings.TrimSpace(creds.Minio.AccessKey)
			sk := strings.TrimSpace(creds.Minio.SecretKey)
			if ep != "" && ak != "" && sk != "" {
				return ep, ak, sk
			}
		}
	}
	for _, svc := range []string{"minio", "rustfs"} {
		ep, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "endpoint")
		if ep == "" {
			ep, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "http")
		}
		ak, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "access_key")
		if ak == "" {
			ak, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "user")
		}
		sk, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "secret_key")
		if sk == "" {
			sk, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "password")
		}
		if ep != "" && ak != "" && sk != "" {
			return strings.TrimRight(ep, "/"), ak, sk
		}
	}
	if n := ctx.Admin.ABCNodes; n != nil {
		ep := strings.TrimRight(n.S3Endpoint, "/")
		ak := n.S3AccessKey
		if ak == "" {
			ak = n.MinioRootUser
		}
		sk := n.S3SecretKey
		if sk == "" {
			sk = n.MinioRootPassword
		}
		return ep, ak, sk
	}
	return "", "", ""
}

func resolveRegion(ctx config.Context) string {
	if r, ok := config.GetAdminFloorField(&ctx.Admin.Services, "minio", "region"); ok && r != "" {
		return r
	}
	if r, ok := config.GetAdminFloorField(&ctx.Admin.Services, "rustfs", "region"); ok && r != "" {
		return r
	}
	if ctx.Admin.ABCNodes != nil && ctx.Admin.ABCNodes.S3Region != "" {
		return ctx.Admin.ABCNodes.S3Region
	}
	return "us-east-1"
}

// splitEndpoint turns an endpoint (with or without scheme) into host[:port] +
// a secure (TLS) flag. Mirrors cmd/data.splitEndpoint.
func splitEndpoint(endpoint string) (host string, secure bool) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			return u.Host, u.Scheme == "https"
		}
	}
	secure = true
	if strings.HasPrefix(endpoint, "localhost") || strings.HasPrefix(endpoint, "127.0.0.1") {
		secure = false
	}
	return endpoint, secure
}
