package credsource

import (
	"context"
	"strings"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// LocalCredSource resolves credentials by reading them directly from a
// config.Context. This is the legacy / default path; it requires no network
// and never blocks. cred_source=="" and cred_source=="local" both map here.
//
// The implementation deliberately does NOT cache: callers may mutate the
// Context (e.g. `abc auth config refresh` replaces the active context) and
// expect the next Resolve to see the new values.
type LocalCredSource struct {
	ctx abccfg.Context
}

// NewLocal builds a LocalCredSource bound to the given context.
func NewLocal(ctx abccfg.Context) *LocalCredSource {
	return &LocalCredSource{ctx: ctx}
}

func (l *LocalCredSource) Name() string { return "local" }

func (l *LocalCredSource) Resolve(_ context.Context) (*Creds, error) {
	c := l.ctx

	out := &Creds{
		Whoami: deriveWhoami(c),
		Source: "local",
	}

	// Nomad creds: prefer admin.services.nomad (the cluster-canonical place);
	// fall back to the context-level access_token + namespace for slot configs
	// that don't carry the admin block.
	if n := c.Admin.Services.Nomad; n != nil {
		out.Nomad.Addr = strings.TrimSpace(n.Addr)
		out.Nomad.Token = strings.TrimSpace(n.Token)
		out.Nomad.Namespace = strings.TrimSpace(n.Namespace)
		out.Nomad.Datacenters = append([]string(nil), n.Datacenters...)
		out.Nomad.HeadPool = strings.TrimSpace(n.HeadPool)
		out.Nomad.WorkerPool = strings.TrimSpace(n.WorkerPool)
	}
	if out.Nomad.Addr == "" {
		out.Nomad.Addr = strings.TrimSpace(c.Endpoint)
	}
	if out.Nomad.Token == "" {
		out.Nomad.Token = strings.TrimSpace(c.AccessToken)
	}
	if out.Nomad.Namespace == "" {
		out.Nomad.Namespace = strings.TrimSpace(c.Namespace)
	}

	// MinIO creds: admin.services.minio is the canonical home.
	if m := c.Admin.Services.MinIO; m != nil {
		out.Minio.Endpoint = strings.TrimSpace(m.Endpoint)
		out.Minio.AccessKey = strings.TrimSpace(m.AccessKey)
		out.Minio.SecretKey = strings.TrimSpace(m.SecretKey)
	}

	return out, nil
}

// deriveWhoami picks the most-specific identity present on the context.
// Mirrors the resolution used elsewhere in the CLI (auth.whoami wins over
// admin.whoami; both empty falls back to "").
func deriveWhoami(c abccfg.Context) string {
	if c.Auth != nil {
		if w := strings.TrimSpace(c.Auth.Whoami); w != "" {
			return w
		}
	}
	if w := strings.TrimSpace(c.Admin.Whoami); w != "" {
		return w
	}
	return ""
}
