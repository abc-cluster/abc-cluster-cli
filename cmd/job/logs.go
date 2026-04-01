package job

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Stream or print logs for a Nomad batch job",
		Args:  cobra.ExactArgs(1),
		RunE:  runLogs,
	}
	cmd.Flags().BoolP("follow", "f", false, "Stream logs in real time")
	cmd.Flags().String("alloc", "", "Filter to a specific allocation ID prefix")
	cmd.Flags().String("task", "main", "Task name within the allocation")
	cmd.Flags().String("type", "stdout", "Log type: stdout or stderr")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().String("since", "", "Show logs since this timestamp (RFC3339)")
	cmd.Flags().String("output", "", "Write stdout logs to file")
	cmd.Flags().String("error", "", "Write stderr logs to file")
	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	follow, _ := cmd.Flags().GetBool("follow")
	allocPrefix, _ := cmd.Flags().GetString("alloc")
	task, _ := cmd.Flags().GetString("task")
	logType, _ := cmd.Flags().GetString("type")
	ns, _ := cmd.Flags().GetString("namespace")
	sinceStr, _ := cmd.Flags().GetString("since")

	if logType != "stdout" && logType != "stderr" {
		return fmt.Errorf("--type must be stdout or stderr, got %q", logType)
	}

	var sinceOffset int64
	if sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			return fmt.Errorf("--since must be RFC3339, got %q: %w", sinceStr, err)
		}
		sinceOffset = t.UnixNano()
	}
	_ = sinceOffset // used by StreamLogs offset in future; always start from 0 for now

	nc := nomadClientFromCmd(cmd)

	// Find the target allocation.
	allocs, err := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)
	if err != nil {
		return fmt.Errorf("getting allocations for job %q: %w", jobID, err)
	}

	if len(allocs) == 0 {
		return fmt.Errorf("no allocations found for job %q", jobID)
	}

	// Filter by prefix if provided.
	var target *NomadAllocStub
	for i := range allocs {
		a := &allocs[i]
		if allocPrefix != "" && !strings.HasPrefix(a.ID, allocPrefix) {
			continue
		}
		// Prefer running allocations; fall back to the most recent.
		if target == nil || a.ClientStatus == "running" {
			target = a
		}
	}
	if target == nil {
		return fmt.Errorf("no allocation matching %q found for job %q", allocPrefix, jobID)
	}

	origin := "start"
	if !follow && sinceStr == "" {
		// Non-follow: read from end to show recent output.
		origin = "end"
	}

	if follow && target.ClientStatus != "running" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Allocation %s is %s; showing completed logs and exiting.\n\n", target.ID[:8], target.ClientStatus)
		follow = false
		origin = "start"
	}

	out := cmd.OutOrStdout()
	if follow {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Streaming logs for alloc %s (task: %s)...\n\n",
			target.ID[:8], task)
	}

	outputPath, _ := cmd.Flags().GetString("output")
	errorPath, _ := cmd.Flags().GetString("error")

	if outputPath != "" || errorPath != "" {
		if outputPath != "" {
			if logType != "stdout" {
				return fmt.Errorf("--output requires --type stdout")
			}
			f, err := os.Create(outputPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := nc.StreamLogs(cmd.Context(), target.ID, task, "stdout", origin, 0, follow, f); err != nil {
				return err
			}
		}
		if errorPath != "" {
			if logType != "stderr" {
				return fmt.Errorf("--error requires --type stderr")
			}
			f, err := os.Create(errorPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := nc.StreamLogs(cmd.Context(), target.ID, task, "stderr", origin, 0, follow, f); err != nil {
				return err
			}
		}
		return nil
	}

	return nc.StreamLogs(cmd.Context(), target.ID, task, logType, origin, 0, follow, out)
}
