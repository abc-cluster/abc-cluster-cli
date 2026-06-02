package portal

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Portal describes a cluster service portal.
//
// Name is the neutral, product-agnostic identifier users type (e.g.
// "job_dashboard"); Service is the underlying technology shown for context
// (e.g. "Nomad"); Aliases keeps the legacy/technical names working.
type Portal struct {
	Name    string   // neutral id: "job_dashboard", "data_browser", …
	Service string   // underlying tech, shown in listings: "Nomad", "MinIO", …
	Label   string   // human-readable label
	Desc    string   // one-line description
	AuthHow string   // "token-url", "magic-link", "creds", "public"
	Aliases []string // legacy/technical names that still resolve (nomad, minio, …)
}

// allPortals defines the fixed ordered list of portals.
var allPortals = []Portal{
	{
		Name:    "job-dashboard",
		Service: "Nomad",
		Label:   "Job dashboard",
		Desc:    "Job scheduler UI — submit, watch, drain workloads",
		AuthHow: "token-url",
		Aliases: []string{"nomad"},
	},
	{
		Name:    "cluster-dashboard",
		Service: "Grafana",
		Label:   "Cluster dashboard",
		Desc:    "Live cluster + per-user resource and job activity dashboards",
		AuthHow: "magic-link",
		Aliases: []string{"grafana"},
	},
	{
		Name:    "workbench",
		Service: "JupyterLab",
		Label:   "Workbench",
		Desc:    "Browser-based JupyterLab for interactive data analysis",
		AuthHow: "magic-link",
		Aliases: []string{"jupyter", "jupyterlab", "lab"},
	},
	{
		Name:    "data-upload",
		Service: "tusd",
		Label:   "Data upload",
		Desc:    "Resumable browser upload — or use abc data upload from the CLI",
		AuthHow: "token-url",
		Aliases: []string{"upload"},
	},
	{
		Name:    "data-browser",
		Service: "MinIO",
		Label:   "Data browser",
		Desc:    "Object storage console — buckets, prefixes, access keys",
		AuthHow: "creds",
		Aliases: []string{"minio", "s3"},
	},
}

// portalNames returns the canonical neutral names, in order.
func portalNames() []string {
	out := make([]string, len(allPortals))
	for i, p := range allPortals {
		out[i] = p.Name
	}
	return out
}

// canonicalPortal resolves any accepted name (neutral or legacy alias) to the
// canonical neutral portal name, or "" if unknown. Case-insensitive and
// separator-insensitive: underscores are treated as hyphens, so both
// "data-browser" and "data_browser" resolve.
func canonicalPortal(name string) string {
	norm := func(s string) string {
		return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
	}
	n := norm(name)
	for _, p := range allPortals {
		if norm(p.Name) == n {
			return p.Name
		}
		for _, a := range p.Aliases {
			if norm(a) == n {
				return p.Name
			}
		}
	}
	return ""
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

// knownServiceLabels are the per-service DNS labels the cluster publishes as
// subdomains of the base cluster domain. When the context endpoint carries one
// of these as its first label (e.g. the Nomad URL), it is stripped to recover
// the bare cluster base domain before portal subdomains are prepended.
var knownServiceLabels = []string{"nomad", "s3", "minio", "workbench", "upload", "grafana", "api"}

// stripServiceLabel returns host with a known leading service label removed:
// "nomad.seedling.abc-cluster.cloud" → "seedling.abc-cluster.cloud".
// A bare base domain ("seedling.abc-cluster.cloud") is returned unchanged.
func stripServiceLabel(host string) string {
	for _, svc := range knownServiceLabels {
		if strings.HasPrefix(host, svc+".") {
			return host[len(svc)+1:]
		}
	}
	return host
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
	host := u.Host // e.g. "seedling.abc-cluster.cloud" OR "nomad.seedling.abc-cluster.cloud"
	scheme := u.Scheme

	// The context endpoint is frequently a service-prefixed URL — the Nomad
	// endpoint (nomad.seedling.abc-cluster.cloud), not the bare cluster base.
	// Strip a known leading service label so portal subdomains resolve to the
	// real hosts (workbench.seedling.abc-cluster.cloud), NOT a doubled-up
	// workbench.nomad.seedling.abc-cluster.cloud that has no DNS record.
	baseHost := stripServiceLabel(host)

	base := func(sub string) string {
		return fmt.Sprintf("%s://%s.%s", scheme, sub, baseHost)
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

// URL returns the base URL for the named portal. Accepts neutral names and
// legacy aliases.
func (p PortalURLs) URL(name string) (string, error) {
	switch canonicalPortal(name) {
	case "job-dashboard":
		return p.Nomad, nil
	case "cluster-dashboard":
		return p.Grafana, nil
	case "workbench":
		return p.Workbench, nil
	case "data-upload":
		return p.Upload, nil
	case "data-browser":
		return p.MinIO, nil
	}
	return "", fmt.Errorf("unknown portal %q — valid: %s", name, strings.Join(portalNames(), ", "))
}
