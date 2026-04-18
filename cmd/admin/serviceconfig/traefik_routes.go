package serviceconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// traefikHostBinding maps a Host(`…`) from the abc-nodes Traefik file provider
// (deployments/abc-nodes/nomad/traefik.nomad.hcl) to an admin.services key.
type traefikHostBinding struct {
	host   string
	svc    string
	field  string // "traefik_http" | "traefik_endpoint"
	upload bool   // also set contexts.<ctx>.upload_endpoint_traefik for tusd
}

// Keep in sync with traefik.nomad.hcl router rules (Host names).
var traefikHostBindings = []traefikHostBinding{
	{host: "minio-console.aither", svc: "minio", field: "traefik_http"},
	{host: "minio.aither", svc: "minio", field: "traefik_endpoint"},
	{host: "grafana.aither", svc: "grafana", field: "traefik_http"},
	{host: "grafana-alloy.aither", svc: "grafana_alloy", field: "traefik_http"},
	{host: "loki.aither", svc: "loki", field: "traefik_http"},
	{host: "prometheus.aither", svc: "prometheus", field: "traefik_http"},
	{host: "ntfy.aither", svc: "ntfy", field: "traefik_http"},
	{host: "vault.aither", svc: "vault", field: "traefik_http"},
	{host: "rustfs.aither", svc: "rustfs", field: "traefik_endpoint"},
	{host: "tusd.aither", svc: "tusd", field: "traefik_http", upload: true},
}

// traefikRouteOverrides probes the cluster Traefik (Traefik CLI + dashboard URL from
// ~/.abc or Nomad) and returns config keys for Host()-style public URLs (scheme +
// hostname) under admin.services.<svc>.traefik_http|traefik_endpoint (and
// contexts.<ctx>.upload_endpoint_traefik for tusd), leaving Nomad-derived http /
// endpoint unchanged.
func traefikRouteOverrides(ctx context.Context, out io.Writer, scheme, nomadAddr, canon string, svcCtx cfg.Context, nc *utils.NomadClient, ctxNomad context.Context, ns string, jobByID map[string]utils.NomadJobStub, skip bool) (map[string]string, []string, error) {
	var notes []string
	if skip {
		return nil, notes, nil
	}
	stub, ok := jobByID["abc-nodes-traefik"]
	if !ok || !strings.EqualFold(stub.Status, "running") {
		return nil, notes, nil
	}

	bin, err := utils.ResolveTraefikBinary()
	if err != nil {
		notes = append(notes, "traefik route sync skipped: "+err.Error())
		return nil, notes, nil
	}
	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		notes = append(notes, fmt.Sprintf("traefik route sync skipped: traefik version: %v", err))
		return nil, notes, nil
	}

	dashboard, ok := traefikDashboardBaseURL(scheme, nomadAddr, svcCtx, nc, ctxNomad, ns, jobByID)
	if !ok {
		notes = append(notes, "traefik route sync skipped: could not resolve dashboard URL (Nomad or admin.services.traefik.http)")
		return nil, notes, nil
	}

	pingEP := "traefik"
	if v, ok := cfg.GetAdminFloorField(&svcCtx.Admin.Services, "traefik", "ping_entrypoint"); ok && strings.TrimSpace(v) != "" {
		pingEP = strings.TrimSpace(v)
	}
	if err := traefikHealthcheckRemote(ctx, bin, dashboard, pingEP); err != nil {
		notes = append(notes, "traefik healthcheck (optional): "+err.Error())
	}

	routersURL := strings.TrimSuffix(dashboard, "/") + "/api/http/routers"
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, routersURL, nil)
	if err != nil {
		return nil, notes, fmt.Errorf("traefik routers request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		notes = append(notes, fmt.Sprintf("traefik route sync skipped: GET %s: %v", routersURL, err))
		return nil, notes, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		notes = append(notes, fmt.Sprintf("traefik route sync skipped: GET %s: %s", routersURL, resp.Status))
		return nil, notes, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, notes, fmt.Errorf("read traefik routers: %w", err)
	}

	hosts, err := hostsFromTraefikRoutersJSON(body)
	if err != nil {
		notes = append(notes, "traefik route sync skipped: parse routers: "+err.Error())
		return nil, notes, nil
	}

	outMap := make(map[string]string)
	for _, h := range hosts {
		for _, b := range traefikHostBindings {
			if b.host != h {
				continue
			}
			u := publicHostURL(scheme, h)
			pfx := "contexts." + canon + ".admin.services." + b.svc + "." + b.field
			outMap[pfx] = u
			if b.upload {
				outMap["contexts."+canon+".upload_endpoint_traefik"] = strings.TrimSuffix(u, "/") + "/files/"
			}
			break
		}
	}
	if len(outMap) == 0 {
		notes = append(notes, "traefik route sync: no matching Host() routers in API response")
		return nil, notes, nil
	}
	notes = append(notes, fmt.Sprintf("traefik route sync: using Traefik CLI + %s (set %d traefik_* / upload_endpoint_traefik keys)", dashboard, len(outMap)))
	return outMap, notes, nil
}

func publicHostURL(scheme, host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host
}

func traefikDashboardBaseURL(scheme, nomadAddr string, svcCtx cfg.Context, nc *utils.NomadClient, ctxNomad context.Context, ns string, jobByID map[string]utils.NomadJobStub) (string, bool) {
	if v, ok := cfg.GetAdminFloorField(&svcCtx.Admin.Services, "traefik", "http"); ok {
		return strings.TrimSuffix(strings.TrimSpace(v), "/"), true
	}
	stub, ok := jobByID["abc-nodes-traefik"]
	if !ok || !strings.EqualFold(stub.Status, "running") {
		return "", false
	}
	allocs, err := nc.GetJobAllocs(ctxNomad, "abc-nodes-traefik", ns, false)
	if err != nil {
		return "", false
	}
	allocID, ok := pickRunningAllocID(allocs)
	if !ok {
		return "", false
	}
	alloc, err := nc.GetAllocation(ctxNomad, allocID, ns)
	if err != nil {
		return "", false
	}
	port, hostIP, ok := lookupDynamicHostPort(alloc, "dashboard")
	if !ok {
		return "", false
	}
	base, err := publicBaseURL(scheme, nomadAddr, hostIP, port)
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimSpace(base), "/"), true
}

func traefikHealthcheckRemote(ctx context.Context, bin, dashboardURL, pingEntryPoint string) error {
	u, err := url.Parse(dashboardURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("parse dashboard URL")
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(host, port)

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("abc-traefik-healthcheck-%d.yml", os.Getpid()))
	content := fmt.Sprintf("ping: {}\nentryPoints:\n  %s:\n    address: %q\n", sanitizeTraefikYAMLKey(pingEntryPoint), addr)
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()

	cmd := exec.CommandContext(ctx, bin, "healthcheck", "--configFile="+tmp)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func hostsFromTraefikRoutersJSON(body []byte) ([]string, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil && len(raw) > 0 {
		return hostsFromRouterSlice(raw)
	}
	var wrap struct {
		Routers []json.RawMessage `json:"routers"`
	}
	if err := json.Unmarshal(body, &wrap); err == nil && len(wrap.Routers) > 0 {
		return hostsFromRouterSlice(wrap.Routers)
	}
	var wrap2 struct {
		HTTPRouters []json.RawMessage `json:"httpRouters"`
	}
	if err := json.Unmarshal(body, &wrap2); err == nil && len(wrap2.HTTPRouters) > 0 {
		return hostsFromRouterSlice(wrap2.HTTPRouters)
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(body, &asMap); err == nil && len(asMap) > 0 {
		raw := make([]json.RawMessage, 0, len(asMap))
		for _, v := range asMap {
			raw = append(raw, v)
		}
		return hostsFromRouterSlice(raw)
	}
	return nil, fmt.Errorf("unrecognized routers JSON shape")
}

func sanitizeTraefikYAMLKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "traefik"
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "traefik"
	}
	return s
}

func hostsFromRouterSlice(items []json.RawMessage) ([]string, error) {
	var hosts []string
	seen := make(map[string]struct{})
	for _, item := range items {
		var r struct {
			Rule string `json:"rule"`
		}
		if err := json.Unmarshal(item, &r); err != nil {
			continue
		}
		h, ok := firstHostFromTraefikRule(r.Rule)
		if !ok {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no Host() rules in routers")
	}
	return hosts, nil
}

func firstHostFromTraefikRule(rule string) (string, bool) {
	// Typical: Host(`grafana.aither`) or Host(`grafana.aither`) && PathPrefix(`/`)
	lr := strings.ToLower(rule)
	idx := strings.Index(lr, "host(`")
	if idx < 0 {
		return "", false
	}
	start := idx + len("host(`")
	end := strings.Index(rule[start:], "`")
	if end < 0 {
		return "", false
	}
	host := rule[start : start+end]
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false
	}
	return host, true
}
