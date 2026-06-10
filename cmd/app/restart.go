package app

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Re-roll a running app without changing its spec",
		Long: `Restart a deployed app: Nomad re-rolls the running allocation in place
(no job-spec change, same image + env). Waits for the Nomad-native health check
to pass, like 'deploy', unless --no-wait.`,
		Args: cobra.ExactArgs(1),
		RunE: runRestart,
	}
	cmd.Flags().Bool("no-wait", false, "Return after triggering the restart without polling health")
	return cmd
}

func runRestart(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}

	// Nomad has no job-level restart HTTP endpoint; restart is a per-allocation
	// client primitive. Restart every running alloc of the job (there is one at
	// the seedling tier).
	allocs, err := nc.GetJobAllocs(ctx, r.JobID, nc.DefaultNamespace(), false)
	if err != nil {
		return fmt.Errorf("list allocations for app %q: %w", r.Name, err)
	}
	restarted := 0
	for i := range allocs {
		if allocs[i].ClientStatus != "running" {
			continue
		}
		if err := nc.RestartAlloc(ctx, allocs[i].ID); err != nil {
			return fmt.Errorf("restart app %q (alloc %s): %w", r.Name, allocs[i].ID, err)
		}
		restarted++
	}
	if restarted == 0 {
		return fmt.Errorf("app %q has no running allocation to restart", r.Name)
	}
	fmt.Fprintf(out, "Restarting %s\n", r.Name)

	noWait, _ := cmd.Flags().GetBool("no-wait")
	if noWait {
		fmt.Fprintln(out, "  (--no-wait: not polling health)")
		return nil
	}

	health := r.Job.Meta["abc_health"]
	if health == "" {
		health = "/"
	}
	if err := waitHealthy(ctx, out, nc, r.JobID, health, defaultHealthTimeout); err != nil {
		if errors.Is(err, errHealthTimeout) {
			return fmt.Errorf(
				"app %q did not become healthy within %s after restart\n"+
					"  • inspect logs: abc app logs %s\n"+
					"  the job was left running for diagnosis",
				r.Name, defaultHealthTimeout, r.Name)
		}
		return err
	}
	fmt.Fprintf(out, "  Healthy. %s\n", appURLFromJob(r))
	return nil
}
