package job

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Nomad batch jobs",
		RunE:  runList,
	}
	cmd.Flags().String("status", "", "Filter by status: running, pending, completed, failed, dead")
	cmd.Flags().String("region", "", "Filter by Nomad region")
	cmd.Flags().String("namespace", "", "Filter by namespace")
	cmd.Flags().Int("limit", 20, "Maximum results to show")
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	ns := namespaceFromCmd(cmd)
	statusFilter, _ := cmd.Flags().GetString("status")
	regionFilter, _ := cmd.Flags().GetString("region")
	limit, _ := cmd.Flags().GetInt("limit")
	sudo := utils.SudoFromCmd(cmd)

	// Widen to all namespaces in sudo mode if not explicitly scoped.
	if sudo && ns == "" {
		ns = "*"
	}

	jobs, err := nc.ListJobs(cmd.Context(), "", ns)
	if err != nil {
		return fmt.Errorf("listing jobs: %w", err)
	}

	// Filter, then sort, then limit — in that order. Truncating Nomad's own
	// ID-ascending order meant `abhinav-<runid>-…` pipeline jobs sorted after
	// `abc-…` and `abhin-script-job-…` and so were never visible at the default
	// limit, no matter how recent. `abc job show` found jobs `abc job list`
	// appeared not to have, which reads as an inconsistency between the two
	// commands rather than as truncation.
	var filtered []NomadJobStub
	for _, j := range jobs {
		// Match against both the raw Nomad status and the friendly display
		// status, so `--status completed`/`failed` (display) and
		// `--status dead`/`running` (raw) all work. Accept "complete" as an
		// alias for "completed".
		if statusFilter != "" {
			ds := displayStatus(j)
			sf := statusFilter
			if strings.EqualFold(sf, "complete") {
				sf = "completed"
			}
			if !strings.EqualFold(j.Status, statusFilter) && !strings.EqualFold(ds, sf) {
				continue
			}
		}
		if regionFilter != "" && !strings.EqualFold(j.Region, regionFilter) {
			continue
		}
		filtered = append(filtered, j)
	}

	// Newest first, so a truncated view shows the runs a user is most likely
	// looking for. Nomad returns jobs ordered by ID.
	sort.SliceStable(filtered, func(a, b int) bool {
		return filtered[a].SubmitTime > filtered[b].SubmitTime
	})

	matched := len(filtered)
	if limit > 0 && matched > limit {
		filtered = filtered[:limit]
	}

	if len(filtered) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No jobs found.")
		return nil
	}

	out := cmd.OutOrStdout()
	now := time.Now()

	if sudo {
		fmt.Fprintf(out, "  %-30s %-16s %-10s %-20s %-18s %-10s\n",
			"NOMAD JOB ID", "NAMESPACE", "STATUS", "DATACENTERS", "SUBMITTED", "DURATION")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 114))
		for _, j := range filtered {
			dcs := strings.Join(j.Datacenters, ",")
			if len(dcs) > 20 {
				dcs = dcs[:17] + "..."
			}
			submitted := ""
			if j.SubmitTime > 0 {
				submitted = time.Unix(0, j.SubmitTime).Format("2006-01-02 15:04")
			}
			ns := j.Namespace
			if ns == "" {
				ns = "default"
			}
			fmt.Fprintf(out, "  %-30s %-16s %-10s %-20s %-18s %-10s\n",
				j.ID, ns, displayStatus(j), dcs, submitted, jobDuration(j, now))
		}
	} else {
		fmt.Fprintf(out, "  %-30s %-10s %-12s %-20s %-18s %-10s\n",
			"NOMAD JOB ID", "STATUS", "REGION", "DATACENTERS", "SUBMITTED", "DURATION")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 106))
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
				j.ID, displayStatus(j), region, dcs, submitted, jobDuration(j, now))
		}
	}

	// Say so when the view is partial. Silently cutting the list made it look
	// like the missing jobs did not exist — a pipeline's worker jobs could be
	// running and greppable via `abc job show` while `abc job list | grep`
	// returned nothing.
	if matched > len(filtered) {
		fmt.Fprintf(out, "\n  Showing %d of %d matching jobs. Use --limit %d (or a larger value) to see the rest.\n",
			len(filtered), matched, matched)
	}
	return nil
}

// displayStatus maps Nomad's raw job status to a label that reads correctly
// for end users. Nomad reports "dead" for any finished job, so a batch job
// that exited 0 looks identical to one that crashed. We disambiguate using the
// per-group allocation summary:
//
//	batch/sysbatch + dead + any Failed/Lost   → "failed"
//	batch/sysbatch + dead + Complete>0        → "completed"
//	otherwise (service stopped, no allocs)    → "dead"
//
// running / pending pass through unchanged.
func displayStatus(j NomadJobStub) string {
	if !strings.EqualFold(j.Status, "dead") {
		return j.Status
	}
	if strings.EqualFold(j.Type, "batch") || strings.EqualFold(j.Type, "sysbatch") {
		var complete, failed, lost int
		for _, tg := range j.JobSummary.Summary {
			complete += tg.Complete
			failed += tg.Failed
			lost += tg.Lost
		}
		switch {
		case failed > 0 || lost > 0:
			return "failed"
		case complete > 0:
			return "completed"
		}
	}
	return "dead"
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
