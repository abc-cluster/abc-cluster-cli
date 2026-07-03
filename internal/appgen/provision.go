package appgen

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	madmin "github.com/minio/madmin-go/v3"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// DataProvisioner creates and revokes the per-app MinIO service account that
// backs an app's `data:` bucket access, and validates that declared buckets
// exist before any Nomad/Vault side effect.
//
// PoC model (phase 1): the service account is a MinIO service account minted
// via the MinIO admin API (madmin-go), scoped by an inline IAM policy to the
// declared buckets. The Vault KV entry referenced in the spec is the intended
// system of record for the credential; in this PoC the credential is minted and
// injected directly into the Nomad job env at submit time. The Vault KV
// write/read is left as a documented operator/hardening step (see the report) —
// the provisioning shape (named SA + scoped policy + revoke on delete) matches
// what the Vault-backed version will do, so swapping the store later is local.
type DataProvisioner struct {
	creds *MinIOCreds
	s3    *minio.Client
	admin *madmin.AdminClient
}

// NewDataProvisioner builds a provisioner from resolved MinIO creds. It
// constructs both a minio-go client (bucket existence) and a madmin-go admin
// client (service-account lifecycle).
func NewDataProvisioner(creds *MinIOCreds) (*DataProvisioner, error) {
	s3, err := minio.New(creds.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
		Secure: creds.Secure,
		Region: creds.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("build storage client: %w", err)
	}
	adm, err := madmin.New(creds.Host, creds.AccessKey, creds.SecretKey, creds.Secure)
	if err != nil {
		return nil, fmt.Errorf("build storage admin client: %w", err)
	}
	return &DataProvisioner{creds: creds, s3: s3, admin: adm}, nil
}

// CheckBuckets verifies every bucket in the spec's data list exists in MinIO.
// It returns the first missing bucket as an error. Run this in step 1 of
// deploy, before any Vault/MinIO/Nomad side effect.
func (p *DataProvisioner) CheckBuckets(ctx context.Context, s *Spec) error {
	for _, d := range s.Data {
		bucket := strings.TrimSpace(d.Bucket)
		ok, err := p.s3.BucketExists(ctx, bucket)
		if err != nil {
			return fmt.Errorf("checking bucket %q: %w", bucket, err)
		}
		if !ok {
			return fmt.Errorf("bucket %q (data entry) does not exist in MinIO — create it or correct the name before deploying", bucket)
		}
	}
	return nil
}

// AppCredentials is the result of Provision — the minted access key + secret
// the app container uses to reach its declared buckets.
type AppCredentials struct {
	AccessKey string
	SecretKey string
}

// Provision creates the per-app MinIO service account scoped to the spec's
// declared buckets and returns its credentials. With no `data:` entries it is
// a no-op (no credentials needed, returns empty AppCredentials, nil) — an app
// that reads nothing from MinIO gets no AWS_* env injected.
//
// The service account access key is deterministic (ServiceAccountName) so a
// re-deploy with the same name+project reuses the identity; the secret is
// minted fresh each time. Deleting a pre-existing SA of the same name first
// keeps re-deploys idempotent.
func (p *DataProvisioner) Provision(ctx context.Context, s *Spec) (*AppCredentials, error) {
	if len(s.Data) == 0 {
		return &AppCredentials{}, nil
	}
	accessKey := s.ServiceAccountName()
	// Idempotent re-deploy: drop any prior SA with this access key first.
	_ = p.admin.DeleteServiceAccount(ctx, accessKey)

	policy, err := bucketPolicy(s.Data)
	if err != nil {
		return nil, err
	}
	// MinIO only auto-generates a secret key when BOTH accessKey and
	// secretKey are omitted. Since accessKey is always set here
	// (deterministic, for idempotent re-deploys), the secret key must be
	// supplied explicitly or some MinIO releases reject the request with
	// "No secret key was provided" — verified against
	// RELEASE.2025-09-07T16-13-09Z (2026-07-03).
	secretKey, err := generateSecretKey()
	if err != nil {
		return nil, fmt.Errorf("generate secret key for %q: %w", accessKey, err)
	}
	out, err := p.admin.AddServiceAccount(ctx, madmin.AddServiceAccountReq{
		AccessKey:   accessKey,
		SecretKey:   secretKey,
		Policy:      policy,
		Description: fmt.Sprintf("abc app %s/%s", s.Project, s.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("create MinIO service account %q: %w", accessKey, err)
	}
	return &AppCredentials{AccessKey: out.AccessKey, SecretKey: out.SecretKey}, nil
}

// generateSecretKey returns a cryptographically random 40-character secret,
// matching the length MinIO itself generates for a fully auto-provisioned
// service account.
func generateSecretKey() (string, error) {
	b := make([]byte, 30) // base64 (4/3 expansion): 30 bytes -> 40 chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Revoke removes the per-app MinIO service account. Called by `abc app delete`
// and by deploy's rollback path when Nomad submission fails after Provision.
// A missing SA is not an error (idempotent cleanup).
func (p *DataProvisioner) Revoke(ctx context.Context, s *Spec) error {
	if len(s.Data) == 0 {
		return nil
	}
	if err := p.admin.DeleteServiceAccount(ctx, s.ServiceAccountName()); err != nil {
		// Treat not-found as success — the goal state (no SA) is reached.
		if strings.Contains(strings.ToLower(err.Error()), "not exist") ||
			strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil
		}
		return fmt.Errorf("revoke MinIO service account %q: %w", s.ServiceAccountName(), err)
	}
	return nil
}

// bucketPolicy builds the inline IAM policy JSON scoping the service account to
// the declared buckets: s3:GetObject for read; + s3:PutObject for read-write.
// ListBucket on the bucket is included so apps can enumerate objects they read.
func bucketPolicy(data []DataMount) (json.RawMessage, error) {
	type stmt struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource []string `json:"Resource"`
	}
	doc := struct {
		Version   string `json:"Version"`
		Statement []stmt `json:"Statement"`
	}{Version: "2012-10-17"}

	for _, d := range data {
		bucket := strings.TrimSpace(d.Bucket)
		access := strings.ToLower(strings.TrimSpace(d.Access))
		if access == "" {
			access = AccessRead
		}
		// Bucket-level: allow listing the bucket.
		doc.Statement = append(doc.Statement, stmt{
			Effect:   "Allow",
			Action:   []string{"s3:ListBucket", "s3:GetBucketLocation"},
			Resource: []string{"arn:aws:s3:::" + bucket},
		})
		// Object-level: read (and optionally write).
		actions := []string{"s3:GetObject"}
		if access == AccessReadWrite {
			actions = append(actions, "s3:PutObject", "s3:DeleteObject")
		}
		doc.Statement = append(doc.Statement, stmt{
			Effect:   "Allow",
			Action:   actions,
			Resource: []string{"arn:aws:s3:::" + bucket + "/*"},
		})
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("build bucket policy: %w", err)
	}
	return json.RawMessage(b), nil
}
