package job

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show details of a Nomad batch job",
		Args:  cobra.ExactArgs(1),
		RunE:  runShow,
	}
	cmd.Flags().String("namespace", "", "Nomad namespace")
	return cmd
}

func runShow(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	nc := nomadClientFromCmd(cmd)
	ns, _ := cmd.Flags().GetString("namespace")

	job, err := nc.GetJob(cmd.Context(), jobID, ns)
	if err != nil {
		return fmt.Errorf("getting job %q: %w", jobID, err)
	}

	allocs, err := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)
	if err != nil {
		// Non-fatal — show what we have.
		allocs = nil
	}

	out := cmd.OutOrStdout()

	submitted := ""
	if job.SubmitTime > 0 {
		submitted = time.Unix(0, job.SubmitTime).Format("2006-01-02 15:04:05")
	}

	fmt.Fprintf(out, "\n  Nomad Job ID   %s\n", job.ID)
	fmt.Fprintf(out, "  Type           %s\n", job.Type)
	fmt.Fprintf(out, "  Status         %s\n", job.Status)
	if job.StatusDescription != "" {
		fmt.Fprintf(out, "  Description    %s\n", job.StatusDescription)
	}
	fmt.Fprintf(out, "  Region         %s\n", job.Region)
	fmt.Fprintf(out, "  Namespace      %s\n", job.Namespace)
	fmt.Fprintf(out, "  Priority       %d\n", job.Priority)
	if len(job.Datacenters) > 0 {
		fmt.Fprintf(out, "  Datacenters    %s\n", strings.Join(job.Datacenters, ", "))
	}
	fmt.Fprintf(out, "  Submitted      %s\n", submitted)

	if len(job.Meta) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  META\n")
		keys := make([]string, 0, len(job.Meta))
		for k := range job.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "  %-20s %s\n", k, job.Meta[k])
		}
	}

	if len(job.TaskGroups) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  TASK GROUPS\n")
		fmt.Fprintf(out, "  %-20s %s\n", "GROUP", "COUNT")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 30))
		for _, tg := range job.TaskGroups {
			fmt.Fprintf(out, "  %-20s %d\n", tg.Name, tg.Count)
		}
	}

	if len(allocs) > 0 {
		// Show up to 10 most recent.
		shown := allocs
		if len(shown) > 10 {
			shown = shown[:10]
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  RECENT ALLOCATIONS\n")
		fmt.Fprintf(out, "  %-12s %-16s %-12s %-10s %-20s\n",
			"ALLOC ID", "NODE", "TASK GROUP", "STATUS", "STARTED")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 75))
		for _, a := range shown {
			started := ""
			if a.CreateTime > 0 {
				started = time.Unix(0, a.CreateTime).Format("2006-01-02 15:04")
			}
			shortID := a.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			node := a.NodeName
			if node == "" {
				node = a.NodeID[:8]
			}
			fmt.Fprintf(out, "  %-12s %-16s %-12s %-10s %-20s\n",
				shortID, node, a.TaskGroup, a.ClientStatus, started)
		}
	}

	fmt.Fprintln(out)
	return nil
}
