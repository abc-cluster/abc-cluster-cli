package job

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Nomad batch jobs",
		RunE:  runList,
	}
	cmd.Flags().String("status", "", "Filter by status: running, complete, dead, pending")
	cmd.Flags().String("region", "", "Filter by Nomad region")
	cmd.Flags().String("namespace", "", "Filter by namespace")
	cmd.Flags().Int("limit", 20, "Maximum results to show")
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	ns, _ := cmd.Flags().GetString("namespace")
	statusFilter, _ := cmd.Flags().GetString("status")
	regionFilter, _ := cmd.Flags().GetString("region")
	limit, _ := cmd.Flags().GetInt("limit")

	jobs, err := nc.ListJobs(cmd.Context(), "", ns)
	if err != nil {
		return fmt.Errorf("listing jobs: %w", err)
	}

	// Filter and limit.
	var filtered []NomadJobStub
	for _, j := range jobs {
		if statusFilter != "" && !strings.EqualFold(j.Status, statusFilter) {
			continue
		}
		if regionFilter != "" && !strings.EqualFold(j.Region, regionFilter) {
			continue
		}
		filtered = append(filtered, j)
		if len(filtered) >= limit {
			break
		}
	}

	if len(filtered) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No jobs found.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-30s %-10s %-12s %-20s %-18s %-10s\n",
		"NOMAD JOB ID", "STATUS", "REGION", "DATACENTERS", "SUBMITTED", "DURATION")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 106))

	now := time.Now()
	for _, j := range filtered {
		dcs := strings.Join(j.Datacenters, ",")
		if len(dcs) > 20 {
			dcs = dcs[:17] + "..."
		}
		submitted := ""
		if j.SubmitTime > 0 {
			submitted = time.Unix(0, j.SubmitTime).Format("2006-01-02 15:04")
		}
		region := j.Region
		if region == "" {
			region = "—"
		}
		fmt.Fprintf(out, "  %-30s %-10s %-12s %-20s %-18s %-10s\n",
			j.ID, j.Status, region, dcs, submitted, jobDuration(j, now))
	}
	return nil
}

// jobDuration returns a human-readable elapsed time for a job.
// Running/pending jobs use SubmitTime→now; stopped jobs use SubmitTime→ModifyTime.
func jobDuration(j NomadJobStub, now time.Time) string {
	if j.SubmitTime == 0 {
		return "—"
	}
	submit := time.Unix(0, j.SubmitTime)
	var end time.Time
	switch j.Status {
	case "running", "pending":
		end = now
	default:
		if j.ModifyTime > j.SubmitTime {
			end = time.Unix(0, j.ModifyTime)
		} else {
			return "—"
		}
	}
	return fmtDuration(end.Sub(submit))
}

// fmtDuration formats a duration as "XhYYm" or "YmZZs" for table display.
func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
