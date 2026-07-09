package job

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace <job-id>",
		Short: "Show a detailed execution trace for a job",
		Long: `Display a structured execution trace for a Nomad job: allocation
placement, per-task lifecycle events (the "why did it fail" detail), and a
short stdout/stderr tail per task.

This is the command to reach for when a job failed fast and 'job logs' is
empty — the task events carry the driver/exit reason even when no logs remain.

  abc job trace nextflow-head-abc123
  abc job trace <job> --alloc <id-prefix>   # one allocation only
  abc job trace <job> --no-logs             # events only, skip the log tail`,
		Args: cobra.ExactArgs(1),
		RunE: runTrace,
	}
	cmd.Flags().String("alloc", "", "Restrict the trace to a specific allocation ID prefix")
	cmd.Flags().Bool("no-logs", false, "Skip the per-task stdout/stderr tail")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	return cmd
}

func runTrace(cmd *cobra.Command, args []string) error {
	jobID, err := resolveJobArg(cmd, args[0])
	if err != nil {
		return err
	}
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)
	out := cmd.OutOrStdout()

	allocs, err := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)
	if err != nil {
		return fmt.Errorf("getting allocations for job %q: %w", jobID, err)
	}

	nsDisplay := ns
	if nsDisplay == "" {
		nsDisplay = "default"
	}
	fmt.Fprintf(out, "\n  TRACE  job %s  (namespace %s)\n", jobID, nsDisplay)

	// No allocations → the interesting signal is WHY nothing placed. Surface the
	// blocked-eval reason instead of an empty trace.
	if len(allocs) == 0 {
		fmt.Fprintf(out, "  No allocations for this job.\n")
		if evals, eerr := nc.GetJobEvals(cmd.Context(), jobID, ns); eerr == nil {
			if reason := placementFailureReason(evals); reason != "" {
				fmt.Fprintf(out, "  Placement blocked: %s\n", reason)
			}
		}
		fmt.Fprintln(out)
		return nil
	}

	allocFilter, _ := cmd.Flags().GetString("alloc")
	noLogs, _ := cmd.Flags().GetBool("no-logs")

	for i := range allocs {
		a := &allocs[i]
		if allocFilter != "" && !strings.HasPrefix(a.ID, allocFilter) {
			continue
		}
		sid := a.ID
		if len(sid) > 8 {
			sid = sid[:8]
		}
		node := a.NodeName
		if node == "" && len(a.NodeID) >= 8 {
			node = a.NodeID[:8]
		}
		fmt.Fprintf(out, "\n  ── alloc %s  [%s]  node=%s  group=%s\n", sid, a.ClientStatus, node, a.TaskGroup)

		taskNames := make([]string, 0, len(a.TaskStates))
		for t := range a.TaskStates {
			taskNames = append(taskNames, t)
		}
		sort.Strings(taskNames)

		for _, tn := range taskNames {
			ts := a.TaskStates[tn]
			state := ts.State
			if ts.Failed {
				state += " (failed)"
			}
			fmt.Fprintf(out, "     task %s: %s\n", tn, state)

			// Last handful of lifecycle events — the failure cause lives here
			// (driver error, exit code, OOM) even when logs are gone.
			events := ts.Events
			const maxEv = 6
			if len(events) > maxEv {
				events = events[len(events)-maxEv:]
			}
			for _, ev := range events {
				msg := strings.TrimSpace(ev.DisplayMessage)
				if msg == "" {
					msg = ev.Type
				}
				fmt.Fprintf(out, "        • %s\n", msg)
			}

			if noLogs {
				continue
			}
			// Best-effort log tail; skipped silently when the alloc dir is gone.
			for _, lt := range []string{"stdout", "stderr"} {
				var buf bytes.Buffer
				if _, lerr := nc.StreamLogs(cmd.Context(), a.ID, tn, lt, "end", 0, false, &buf); lerr != nil {
					continue
				}
				tail := strings.TrimRight(buf.String(), "\n")
				if tail == "" {
					continue
				}
				fmt.Fprintf(out, "        %s tail:\n", lt)
				for _, line := range lastLines(tail, 8) {
					fmt.Fprintf(out, "          | %s\n", line)
				}
			}
		}
	}
	fmt.Fprintln(out)
	return nil
}

// lastLines returns the final n lines of s (all of them if fewer).
func lastLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
