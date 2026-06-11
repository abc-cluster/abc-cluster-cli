package app

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
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
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 116))

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
// (public/shared/private, canonical order) and a primary URL. public → the full
// public-edge https URL; private/shared → the /apps/<app>/ path (the door host —
// campus IP / overlay — is operator infra, not known to the CLI).
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
	var hasPublic, hasShared, hasPrivate bool
	var pubURL, pathURL string
	for r, rule := range rules {
		switch {
		case strings.HasPrefix(rule, "Host("):
			hasPublic = true
			h := strings.TrimSuffix(strings.TrimPrefix(rule, "Host(`"), "`)")
			pubURL = "https://" + h + "/"
		case strings.HasPrefix(rule, "PathPrefix("):
			p := strings.TrimSuffix(strings.TrimPrefix(rule, "PathPrefix(`"), "`)")
			pathURL = p + "/"
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
	if len(pl) == 0 {
		return "—", "—"
	}
	u := pubURL
	if u == "" {
		u = pathURL
	}
	return strings.Join(pl, ","), u
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
