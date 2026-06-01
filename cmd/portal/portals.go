package portal

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Portal describes a cluster service portal.
type Portal struct {
	Name    string // "nomad", "grafana", etc.
	Label   string // human-readable
	Desc    string // one-line description
	AuthHow string // "token-url", "magic-link", "creds", "public"
}

// allPortals defines the fixed ordered list of portals.
var allPortals = []Portal{
	{
		Name:    "nomad",
		Label:   "Nomad",
		Desc:    "Job scheduler UI — submit, watch, drain workloads",
		AuthHow: "token-url",
	},
	{
		Name:    "grafana",
		Label:   "Grafana",
		Desc:    "Live cluster + per-user resource and job activity dashboards",
		AuthHow: "magic-link",
	},
	{
		Name:    "workbench",
		Label:   "Workbench",
		Desc:    "Browser-based JupyterLab for interactive data analysis",
		AuthHow: "magic-link",
	},
	{
		Name:    "upload",
		Label:   "Upload",
		Desc:    "Resumable browser upload — or use abc data upload from the CLI",
		AuthHow: "token-url",
	},
	{
		Name:    "minio",
		Label:   "MinIO",
		Desc:    "Object storage console — buckets, prefixes, access keys",
		AuthHow: "creds",
	},
}

// PortalURLs holds all portal URLs derived from the active context.
type PortalURLs struct {
	Nomad     string
	Grafana   string
	Workbench string
	Upload    string
	MinIO     string
	// authSvc is the workbench URL prefix used for /auth/* routes
	authSvc string
}

// DeriveURLs derives all portal URLs from the active context.
// The context endpoint (e.g. https://seedling.abc-cluster.cloud) is the
// canonical base; subdomains are prepended for each portal. NomadAddr()
// is used for the Nomad URL since it already is the public-facing URL.
func DeriveURLs(ctx config.Context) (PortalURLs, error) {
	ep := strings.TrimRight(ctx.Endpoint, "/")
	if ep == "" {
		return PortalURLs{}, fmt.Errorf("active context has no endpoint set")
	}

	u, err := url.Parse(ep)
	if err != nil {
		return PortalURLs{}, fmt.Errorf("invalid context endpoint %q: %w", ep, err)
	}
	host := u.Host // e.g. "seedling.abc-cluster.cloud"
	scheme := u.Scheme

	base := func(sub string) string {
		return fmt.Sprintf("%s://%s.%s", scheme, sub, host)
	}

	// Nomad: use NomadAddr() directly — it IS the public nomad URL.
	nomadURL := strings.TrimRight(ctx.NomadAddr(), "/")
	if nomadURL == "" {
		nomadURL = base("nomad")
	}

	workbenchURL := base("workbench")
	grafanaURL := base("grafana")
	uploadURL := ctx.UploadEndpoint
	if uploadURL == "" {
		uploadURL = base("upload")
	}
	uploadURL = strings.TrimRight(uploadURL, "/")
	minioURL := base("minio")

	return PortalURLs{
		Nomad:     nomadURL,
		Grafana:   grafanaURL,
		Workbench: workbenchURL,
		Upload:    uploadURL,
		MinIO:     minioURL,
		authSvc:   workbenchURL, // /auth/* routes served by workbench Caddy
	}, nil
}

// AuthSvcBase returns the base URL for abc-auth-svc /auth/* endpoints.
// These are served by the workbench Caddy which proxies /auth/* to abc-auth-svc.
func (p PortalURLs) AuthSvcBase() string {
	return p.authSvc
}

// URL returns the base URL for the named portal.
func (p PortalURLs) URL(name string) (string, error) {
	switch name {
	case "nomad":
		return p.Nomad, nil
	case "grafana":
		return p.Grafana, nil
	case "workbench":
		return p.Workbench, nil
	case "upload":
		return p.Upload, nil
	case "minio":
		return p.MinIO, nil
	}
	return "", fmt.Errorf("unknown portal %q — valid: nomad, grafana, workbench, upload, minio", name)
}
