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
	cmd.Flags().String("namespace", "", "Filter by namespace")
	cmd.Flags().Int("limit", 20, "Maximum results to show")
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	ns, _ := cmd.Flags().GetString("namespace")
	statusFilter, _ := cmd.Flags().GetString("status")
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
	fmt.Fprintf(out, "  %-30s %-10s %-10s %-20s %-12s\n",
		"NOMAD JOB ID", "STATUS", "REGION", "DATACENTERS", "SUBMITTED")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 90))

	for _, j := range filtered {
		dcs := strings.Join(j.Datacenters, ",")
		if len(dcs) > 20 {
			dcs = dcs[:17] + "..."
		}
		submitted := ""
		if j.SubmitTime > 0 {
			t := time.Unix(0, j.SubmitTime)
			submitted = t.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(out, "  %-30s %-10s %-10s %-20s %-12s\n",
			j.ID, j.Status, j.Namespace, dcs, submitted)
	}
	return nil
}
