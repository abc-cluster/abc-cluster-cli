package app

import (
	"fmt"
	"strings"
	"time"

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

	fmt.Fprintf(out, "  %-26s %-9s %-12s %-20s %-10s %s\n",
		"NAME", "STATUS", "PROJECT", "IMAGE", "UPTIME", "URL")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 110))

	count := 0
	for i := range stubs {
		if !strings.HasPrefix(stubs[i].ID, appJobPrefix) {
			continue
		}
		count++
		job, err := nc.GetJob(ctx, stubs[i].ID, nc.DefaultNamespace())
		name := strings.TrimPrefix(stubs[i].ID, appJobPrefix)
		project, image, url := "—", "—", "—"
		if err == nil {
			if v := job.Meta["abc_app"]; v != "" {
				name = v
			}
			project = orDash(job.Meta["abc_project"])
			image = shortImage(taskImage(job))
			if project != "—" {
				url = "https://" + project + "-" + name + ".apps.seedling.abc-cluster.cloud"
			}
		}
		status := appStatus(stubs[i])
		uptime := uptimeSince(stubs[i].SubmitTime)
		fmt.Fprintf(out, "  %-26s %-9s %-12s %-20s %-10s %s\n",
			name, status, project, image, uptime, url)
	}
	if count == 0 {
		fmt.Fprintf(out, "  (no apps deployed in namespace %q)\n", nc.DefaultNamespace())
	}
	return nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func shortImage(img string) string {
	if img == "" {
		return "—"
	}
	// Show repo:tag, drop the registry host for width.
	if i := strings.LastIndex(img, "/"); i >= 0 {
		return img[i+1:]
	}
	return img
}

func uptimeSince(submitNanos int64) string {
	if submitNanos <= 0 {
		return "—"
	}
	d := time.Since(time.Unix(0, submitNanos))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
