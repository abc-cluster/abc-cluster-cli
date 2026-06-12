package app

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List deployed apps",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	stubs, err := nc.ListJobs(ctx, appJobPrefix, nc.DefaultNamespace())
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}

	fmt.Fprintf(out, "  %-30s %-9s %-14s %-22s %s\n",
		"NAME", "STATUS", "PROJECT", "EXPOSE", "URL")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 140))

	count := 0
	for i := range stubs {
		if !strings.HasPrefix(stubs[i].ID, appJobPrefix) {
			continue
		}
		count++
		job, err := nc.GetJob(ctx, stubs[i].ID, nc.DefaultNamespace())
		name := strings.TrimPrefix(stubs[i].ID, appJobPrefix)
		project, expose, url := "—", "—", "—"
		if err == nil {
			if v := job.Meta["abc_app"]; v != "" {
				name = v
			}
			project = orDash(job.Meta["abc_project"])
			expose, url = appExpose(job)
		}
		status := appStatus(stubs[i])
		fmt.Fprintf(out, "  %-30s %-9s %-14s %-22s %s\n",
			name, status, project, expose, url)
	}
	if count == 0 {
		fmt.Fprintf(out, "  (no apps deployed in namespace %q)\n", nc.DefaultNamespace())
	}
	return nil
}

// appExpose parses an app job's Traefik routing tags into its exposure planes
// (public/shared/private, canonical order) and a clickable primary URL:
//
//   - public  → https://<subdomain>.apps.seedling.abc-cluster.cloud/   (AppsDomain)
//   - private → https://<PrivateAppsDoor>/apps/<subdomain>/             (campus-LAN door)
//   - shared  → https://<SharedAppsDoor>/apps/<subdomain>/  (or private door if
//                                                            shared not wired yet)
//   - legacy Host(internal-only) → https://<that-host>/  (pre-expose deployments)
//
// Plane label order is canonical (public, shared, private). The URL prefers
// public > private > shared > legacy so the displayed link is the most
// stable / widely-reachable surface.
func appExpose(job *utils.NomadJob) (planes, url string) {
	if job == nil {
		return "—", "—"
	}
	rules := map[string]string{} // router-prefix → rule
	eps := map[string]string{}   // router-prefix → entrypoints
	for _, g := range job.TaskGroups {
		for _, svc := range g.Services {
			for _, tag := range svc.Tags {
				if r, v, ok := strings.Cut(tag, ".rule="); ok {
					rules[r] = v
				}
				if r, v, ok := strings.Cut(tag, ".entrypoints="); ok {
					eps[r] = v
				}
			}
		}
	}
	var hasPublic, hasShared, hasPrivate, hasLegacy bool
	var pubURL, pathPart, legacyURL string
	for r, rule := range rules {
		switch {
		case strings.Contains(rule, "Host("):
			// One or more Host(`h`) ORed. Take the first host. A host under the
			// public wildcard is the `public` plane; any other host is a legacy
			// internal route (tailnet / campus name) pre-dating the expose scheme.
			h := firstQuoted(rule, "Host(`")
			if strings.HasSuffix(h, appgen.AppsDomain) {
				hasPublic = true
				pubURL = "https://" + h + "/"
			} else if h != "" {
				hasLegacy = true
				if legacyURL == "" {
					legacyURL = "https://" + h + "/"
				}
			}
		case strings.Contains(rule, "PathPrefix("):
			pathPart = firstQuoted(rule, "PathPrefix(`") + "/"
			ep := eps[r]
			hasPrivate = hasPrivate || strings.Contains(ep, "private")
			hasShared = hasShared || strings.Contains(ep, "shared")
		}
	}
	pl := make([]string, 0, 3)
	if hasPublic {
		pl = append(pl, "public")
	}
	if hasShared {
		pl = append(pl, "shared")
	}
	if hasPrivate {
		pl = append(pl, "private")
	}
	if hasLegacy && !hasShared && !hasPrivate {
		pl = append(pl, "internal*") // legacy Host-routed internal (pre-expose)
	}
	if len(pl) == 0 {
		return "—", "—"
	}
	// URL selection.
	u := pubURL
	if u == "" && pathPart != "" {
		switch {
		case hasPrivate:
			u = "https://" + appgen.PrivateAppsDoor + pathPart
		case hasShared:
			if appgen.SharedAppsDoor != "" {
				u = "https://" + appgen.SharedAppsDoor + pathPart
			} else {
				// shared-only but no shared door wired — fall back to private door
				// (Traefik PathPrefix matches on either entrypoint, so the campus-
				// LAN door reaches the same backing service for users who can hit
				// it). The "shared" plane label still appears in the EXPOSE column.
				u = "https://" + appgen.PrivateAppsDoor + pathPart
			}
		}
	}
	if u == "" {
		u = legacyURL
	}
	return strings.Join(pl, ","), u
}

// firstQuoted extracts the first backtick-quoted argument after a matcher prefix,
// e.g. firstQuoted("Host(`a`) || Host(`b`)", "Host(`") == "a".
func firstQuoted(rule, prefix string) string {
	i := strings.Index(rule, prefix)
	if i < 0 {
		return ""
	}
	rest := rule[i+len(prefix):]
	if j := strings.Index(rest, "`"); j >= 0 {
		return rest[:j]
	}
	return ""
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
