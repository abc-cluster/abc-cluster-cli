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
		allocs = nil // non-fatal
	}

	out := cmd.OutOrStdout()
	now := time.Now()

	submitted := ""
	if job.SubmitTime > 0 {
		submitted = time.Unix(0, job.SubmitTime).Format("2006-01-02 15:04:05")
	}

	// Compute job duration from SubmitTime.
	dur := ""
	if job.SubmitTime > 0 {
		var end time.Time
		switch job.Status {
		case "running", "pending":
			end = now
		default:
			// Use the latest alloc ModifyTime as a proxy for job end time.
			var latest int64
			for _, a := range allocs {
				if a.ModifyTime > latest {
					latest = a.ModifyTime
				}
			}
			if latest > job.SubmitTime {
				end = time.Unix(0, latest)
			}
		}
		if !end.IsZero() {
			dur = fmtDuration(end.Sub(time.Unix(0, job.SubmitTime)))
		}
	}

	// Extract driver from first task of first task group.
	driver := ""
	for _, tg := range job.TaskGroups {
		if len(tg.Tasks) > 0 {
			driver = tg.Tasks[0].Driver
			break
		}
	}

	fmt.Fprintf(out, "\n  Nomad Job ID   %s\n", job.ID)
	fmt.Fprintf(out, "  Type           %s\n", job.Type)
	fmt.Fprintf(out, "  Status         %s\n", job.Status)
	if job.StatusDescription != "" {
		fmt.Fprintf(out, "  Description    %s\n", job.StatusDescription)
	}
	fmt.Fprintf(out, "  Region         %s\n", job.Region)
	fmt.Fprintf(out, "  Namespace      %s\n", job.Namespace)
	if len(job.Datacenters) > 0 {
		fmt.Fprintf(out, "  Datacenter     %s\n", strings.Join(job.Datacenters, ", "))
	}
	if driver != "" {
		fmt.Fprintf(out, "  Driver         %s\n", driver)
	}
	fmt.Fprintf(out, "  Priority       %d\n", job.Priority)
	fmt.Fprintf(out, "  Submitted      %s\n", submitted)
	if dur != "" {
		fmt.Fprintf(out, "  Duration       %s\n", dur)
	}

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
		// Compute per-group alloc counts from allocation list.
		type groupCounts struct{ desired, running, succeeded, failed int }
		counts := make(map[string]*groupCounts, len(job.TaskGroups))
		for _, tg := range job.TaskGroups {
			counts[tg.Name] = &groupCounts{desired: tg.Count}
		}
		for _, a := range allocs {
			c, ok := counts[a.TaskGroup]
			if !ok {
				continue
			}
			switch a.ClientStatus {
			case "running":
				c.running++
			case "complete":
				c.succeeded++
			case "failed", "lost":
				c.failed++
			}
		}

		fmt.Fprintln(out)
		fmt.Fprintf(out, "  TASK GROUPS\n")
		fmt.Fprintf(out, "  %-20s %-8s %-8s %-10s %-8s\n",
			"GROUP", "DESIRED", "RUNNING", "SUCCEEDED", "FAILED")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 58))
		for _, tg := range job.TaskGroups {
			c := counts[tg.Name]
			fmt.Fprintf(out, "  %-20s %-8d %-8d %-10d %-8d\n",
				tg.Name, c.desired, c.running, c.succeeded, c.failed)
		}
	}

	if len(allocs) > 0 {
		shown := allocs
		if len(shown) > 10 {
			shown = shown[:10]
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  RECENT ALLOCATIONS\n")
		fmt.Fprintf(out, "  %-12s %-16s %-12s %-10s %-18s %-10s\n",
			"ALLOC ID", "NODE", "TASK GROUP", "STATUS", "STARTED", "DURATION")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 84))
		for _, a := range shown {
			started := ""
			if a.CreateTime > 0 {
				started = time.Unix(0, a.CreateTime).Format("2006-01-02 15:04")
			}
			allocDur := ""
			if a.CreateTime > 0 {
				var allocEnd time.Time
				switch a.ClientStatus {
				case "running":
					allocEnd = now
				default:
					if a.ModifyTime > a.CreateTime {
						allocEnd = time.Unix(0, a.ModifyTime)
					}
				}
				if !allocEnd.IsZero() {
					allocDur = fmtDuration(allocEnd.Sub(time.Unix(0, a.CreateTime)))
				}
			}
			shortID := a.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			node := a.NodeName
			if node == "" && len(a.NodeID) >= 8 {
				node = a.NodeID[:8]
			}
			fmt.Fprintf(out, "  %-12s %-16s %-12s %-10s %-18s %-10s\n",
				shortID, node, a.TaskGroup, a.ClientStatus, started, allocDur)
		}
	}

	fmt.Fprintln(out)
	return nil
}
