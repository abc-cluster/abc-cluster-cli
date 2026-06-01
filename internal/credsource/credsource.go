// Package credsource is the credential-resolution layer for the abc CLI.
//
// Each context in ~/.abc/config.yaml carries a top-level `cred_source` field
// that names the credential-resolution mode:
//
//   - ""        — default; treated the same as "local".
//   - "local"   — read real Nomad/MinIO creds directly from the context's
//                 fields. This is today's behaviour; LocalCredSource wraps it.
//   - "seedling/v1", "grove/v1", "cloud/v1" — resolve real creds on demand
//                 by calling the named tier's broker (POST /auth/exchange).
//
// Resolver implementations live in this package (one per `cred_source` value).
// Callers obtain a resolver via Select(ctx) and never read the credential
// fields on config.Context directly — that lets the same calling code work
// against any resolver.
//
// Design: brainstorms/abc-seedling-onboarding/2026-06-01-opaque-tokens-credential-broker.md
package credsource

import (
	"context"
	"fmt"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Creds is the materialised credential bundle a CredSource returns. All
// resolvers normalise into this shape so call sites can use one type.
type Creds struct {
	// Whoami is the identity associated with the context (e.g. "slot-calm_dassie"
	// for a pool slot, "abhi" for a named admin). Always populated.
	Whoami string

	// Nomad is the cluster-side compute backend.
	Nomad NomadCreds

	// Minio is the cluster-side S3-compatible storage backend.
	Minio MinioCreds

	// Source names the resolver that produced this bundle (e.g. "local",
	// "seedling/v1"). Useful for diagnostics + audit log entries.
	Source string
}

type NomadCreds struct {
	Addr        string
	Token       string
	Namespace   string
	Datacenters []string
	HeadPool    string
	WorkerPool  string
}

type MinioCreds struct {
	Endpoint  string
	AccessKey string
	SecretKey string
}

// CredSource is the resolver interface. Each implementation knows how to
// turn its bound state (typically a config.Context plus any cached state)
// into a Creds bundle that callers can use against Nomad/MinIO.
type CredSource interface {
	// Resolve returns the credentials, honouring ctx for cancellation. Local
	// resolvers never block on the network; broker resolvers may make an
	// HTTP call on cache miss.
	Resolve(ctx context.Context) (*Creds, error)

	// Name returns the cred_source string the resolver implements (e.g.
	// "local", "seedling/v1"). Used by callers for logs and diagnostics.
	Name() string
}

// Select returns the appropriate CredSource for the given context. Today
// it dispatches on context.CredSource; unknown values return an error so
// the caller can show a clear message ("update abc CLI to a version that
// understands cred_source: <value>").
//
// Empty cred_source is treated as "local" for back-compat with configs
// rendered before Phase 1.
func Select(ctx abccfg.Context) (CredSource, error) {
	switch normalize(ctx.CredSource) {
	case "", "local":
		return NewLocal(ctx), nil
	case "seedling/v1":
		return NewSeedlingV1(ctx)
	case "grove/v1":
		return nil, fmt.Errorf("cred_source %q: GroveV1CredSource not yet implemented (Phase 3, when the grove tier ships)", ctx.CredSource)
	case "cloud/v1":
		return nil, fmt.Errorf("cred_source %q: CloudV1CredSource not yet implemented (Phase 3, when the cloud tier ships)", ctx.CredSource)
	default:
		return nil, fmt.Errorf("unknown cred_source %q (recognised: local, seedling/v1, grove/v1, cloud/v1)", ctx.CredSource)
	}
}

// normalize lowercases + trims; we want "Local" / " local " / "LOCAL" all to
// match. cred_source values are intentionally lowercase by convention.
func normalize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
