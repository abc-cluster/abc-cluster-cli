package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
)

// appJobPrefix is the Nomad job-name prefix every app job carries.
const appJobPrefix = "app-"

// resolvedApp carries the Nomad job + parsed identity for a deployed app.
type resolvedApp struct {
	JobID     string // app-<project>-<name>
	Name      string // <name> (from meta.abc_app)
	Project   string // <project> (from meta.abc_project)
	Framework string // from meta.abc_framework
	Job       *utils.NomadJob
}

// resolveApp finds the app job for a user-supplied short name in the app
// namespace. Apps are addressed by their short `<name>` (not the full
// `app-<project>-<name>` job id). It lists app jobs and matches on the
// meta.abc_app value; if exactly one matches, it is returned. Ambiguity (two
// projects with the same app name) or no match is a clear error, never a panic.
func resolveApp(ctx context.Context, nc *utils.NomadClient, name string) (*resolvedApp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("app name is required")
	}
	stubs, err := nc.ListJobs(ctx, appJobPrefix, nc.DefaultNamespace())
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	var matches []*resolvedApp
	for i := range stubs {
		if !strings.HasPrefix(stubs[i].ID, appJobPrefix) {
			continue
		}
		job, err := nc.GetJob(ctx, stubs[i].ID, nc.DefaultNamespace())
		if err != nil {
			continue
		}
		appName := job.Meta["abc_app"]
		// Fall back to suffix match when meta is missing (older deploys).
		if appName == "" {
			appName = strings.TrimPrefix(stubs[i].ID, appJobPrefix)
		}
		if appName == name || stubs[i].ID == appJobPrefix+job.Meta["abc_project"]+"-"+name {
			matches = append(matches, &resolvedApp{
				JobID:     stubs[i].ID,
				Name:      name,
				Project:   job.Meta["abc_project"],
				Framework: job.Meta["abc_framework"],
				Job:       job,
			})
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("app %q not found in namespace %q", name, nc.DefaultNamespace())
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.JobID)
		}
		return nil, fmt.Errorf("app name %q is ambiguous across projects: %s — delete by full job id or disambiguate the project", name, strings.Join(ids, ", "))
	}
}

// appStatus derives a coarse running/stopped/failed status from a job stub's
// summary. A stopped job (status "dead", no running allocs, no failures) reads
// "stopped"; a job with failed allocs and none running reads "failed".
func appStatus(stub utils.NomadJobStub) string {
	if strings.EqualFold(stub.Status, "running") {
		return "running"
	}
	running, failed := 0, 0
	for _, g := range stub.JobSummary.Summary {
		running += g.Running + g.Starting
		failed += g.Failed
	}
	switch {
	case running > 0:
		return "running"
	case failed > 0:
		return "failed"
	default:
		return "stopped"
	}
}

// taskImage returns the docker image of the app task, or "" if not found.
func taskImage(job *utils.NomadJob) string {
	for _, g := range job.TaskGroups {
		for _, t := range g.Tasks {
			if t.Config != nil {
				if img, ok := t.Config["image"].(string); ok {
					return img
				}
			}
		}
	}
	return ""
}

// appURLFromJob reconstructs the external app URL from a resolved app's meta.
func appURLFromJob(r *resolvedApp) string {
	if r.Job != nil {
		if u := r.Job.Meta["abc_url"]; u != "" {
			return u
		}
	}
	if r.Project == "" {
		return ""
	}
	return "https://" + r.Project + "-" + r.Name + "." + appgen.AppsDomain
}
