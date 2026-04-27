package pipeline

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs <name>",
		Short: "List recent runs for a pipeline",
		Long: `Show a history of recent Nomad job submissions for the named pipeline.

Each row shows the run UUID, job ID, status, when it started, its duration, and
the node it ran on. Runs are sorted by submission time, newest first.

  abc pipeline runs rnaseq
  abc pipeline runs rnaseq --limit 5
  abc pipeline runs rnaseq --json`,
		Args: cobra.ExactArgs(1),
		RunE: runRuns,
	}
	cmd.Flags().Int("limit", 10, "Maximum number of runs to display")
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

type runRecord struct {
	RunUUID  string `json:"run_uuid"`
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Started  string `json:"started"`
	Duration string `json:"duration"`
	Node     string `json:"node"`
}

func runRuns(cmd *cobra.Command, args []string) error {
	name := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)
	limit, _ := cmd.Flags().GetInt("limit")
	asJSON, _ := cmd.Flags().GetBool("json")

	ctx := cmd.Context()

	// List jobs whose ID starts with the pipeline name.
	jobs, err := nc.ListJobs(ctx, name, ns)
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}

	// Sort by SubmitTime descending (most recent first).
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].SubmitTime > jobs[j].SubmitTime
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}

	var records []runRecord
	for _, stub := range jobs {
		// Fetch full job to read Meta.run_uuid.
		job, err := nc.GetJob(ctx, stub.ID, ns)
		if err != nil {
			// Non-fatal: include the row with empty UUID.
			job = &utils.NomadJob{ID: stub.ID, Status: stub.Status}
		}

		runUUID := ""
		if job.Meta != nil {
			runUUID = job.Meta["run_uuid"]
		}

		// Fetch most recent allocation for timing and node info.
		allocs, err := nc.GetJobAllocs(ctx, stub.ID, ns, false)

		var started, duration, node string
		if err == nil && len(allocs) > 0 {
			// Pick the most recently created alloc.
			best := allocs[0]
			for _, a := range allocs[1:] {
				if a.CreateTime > best.CreateTime {
					best = a
				}
			}
			node = best.NodeName
			if best.CreateTime > 0 {
				t := time.Unix(0, best.CreateTime)
				started = t.Format("2006-01-02 15:04:05")
				end := time.Unix(0, best.ModifyTime)
				if best.ModifyTime > best.CreateTime && isAllocTerminal(best.ClientStatus) {
					duration = end.Sub(t).Round(time.Second).String()
				} else if !isAllocTerminal(best.ClientStatus) {
					duration = time.Since(t).Round(time.Second).String() + " (running)"
				}
			}
		}

		records = append(records, runRecord{
			RunUUID:  runUUID,
			JobID:    stub.ID,
			Status:   stub.Status,
			Started:  started,
			Duration: duration,
			Node:     node,
		})
	}

	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}

	if len(records) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  No runs found for pipeline %q\n", name)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  RUN UUID\tJOB ID\tSTATUS\tSTARTED\tDURATION\tNODE")
	fmt.Fprintln(w, "  "+strings.Repeat("-", 8)+"\t"+strings.Repeat("-", 6)+"\t"+strings.Repeat("-", 6)+"\t"+strings.Repeat("-", 7)+"\t"+strings.Repeat("-", 8)+"\t"+strings.Repeat("-", 4))
	for _, r := range records {
		uid := r.RunUUID
		if len(uid) > 8 {
			uid = uid[:8]
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			uid, r.JobID, r.Status, r.Started, r.Duration, r.Node)
	}
	return w.Flush()
}

func isAllocTerminal(status string) bool {
	return status == "complete" || status == "failed" || status == "lost"
}
