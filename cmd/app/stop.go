package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop an app (preserves the job definition)",
		Long: `Stop a deployed app: scales it to zero running allocations but preserves
the Nomad job definition so it can be re-deployed. The app's MinIO service
account is left in place (use 'abc app delete' to fully remove an app and
revoke its data credentials).`,
		Args: cobra.ExactArgs(1),
		RunE: runStop,
	}
}

func runStop(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}
	// purge=false → preserve the job definition.
	if _, err := nc.StopJob(ctx, r.JobID, nc.DefaultNamespace(), false); err != nil {
		return fmt.Errorf("stop app %q: %w", r.Name, err)
	}
	fmt.Fprintf(out, "Stopped %s\n", r.Name)
	if u := appURLFromJob(r); u != "" {
		fmt.Fprintf(out, "  URL (for re-deploy reference): %s\n", u)
	}
	return nil
}
