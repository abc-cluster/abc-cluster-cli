package app

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// appTaskName is the task name every app job uses (see hclgen.go).
const appTaskName = "app"

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <name> -- <cmd...>",
		Short: "Run a one-off command in a running app container",
		Long: `Run a one-off command in the app's running container (wraps
'nomad alloc exec -task app'). Pass the command after a '--' separator:

  abc app exec sucuri -- ls -la /app
  abc app exec sucuri -it -- /bin/sh      # interactive shell (TTY)

Owner/admin only at the seedling tier.`,
		Args: cobra.MinimumNArgs(2),
		RunE: runExec,
		// The command after -- is arbitrary; don't let cobra parse its flags.
		DisableFlagParsing: false,
	}
	cmd.Flags().BoolP("interactive", "i", false, "Keep stdin open (interactive)")
	cmd.Flags().BoolP("tty", "t", false, "Allocate a pseudo-TTY")
	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	name := args[0]
	command := args[1:]
	if len(command) == 0 {
		return fmt.Errorf("no command given; usage: abc app exec %s -- <cmd...>", name)
	}

	r, err := resolveApp(ctx, nc, name)
	if err != nil {
		return err
	}

	// Find a running alloc to exec into.
	allocs, err := nc.GetJobAllocs(ctx, r.JobID, nc.DefaultNamespace(), false)
	if err != nil {
		return fmt.Errorf("list allocations for app %q: %w", r.Name, err)
	}
	var allocID string
	for i := range allocs {
		if allocs[i].ClientStatus == "running" {
			allocID = allocs[i].ID
			break
		}
	}
	if allocID == "" {
		return fmt.Errorf("app %q has no running allocation to exec into", r.Name)
	}

	interactive, _ := cmd.Flags().GetBool("interactive")
	tty, _ := cmd.Flags().GetBool("tty")

	// Build: nomad alloc exec -namespace <ns> [-i] [-t] -task app <alloc> <cmd...>
	nomadArgs := []string{"alloc", "exec", "-namespace", nc.DefaultNamespace()}
	if interactive {
		nomadArgs = append(nomadArgs, "-i")
	}
	if tty {
		nomadArgs = append(nomadArgs, "-t")
	}
	nomadArgs = append(nomadArgs, "-task", appTaskName, allocID)
	nomadArgs = append(nomadArgs, command...)

	// Wire the local TTY through so -it shells work. Critically, exec must honour
	// the exact addr/token/region the rest of `abc app` resolved into nc (flags /
	// env / context), not whatever the active config context happens to hold — a
	// scoped context token would otherwise yield a 403 on abc-apps.
	return utils.RunNomadCLI(ctx, nomadArgs, "", nc.Addr(), nc.Token(), nc.Region(),
		os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
