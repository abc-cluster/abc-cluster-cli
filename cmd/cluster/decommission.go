package cluster

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDecommissionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decommission <name>",
		Short: "Drain and remove a cluster from the fleet (requires --cloud)",
		Args:  cobra.ExactArgs(1),
		RunE:  runClusterDecommission,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("drain", true, "Drain all jobs before decommissioning (default: true)")
	cmd.Flags().String("deadline", "2h", "Maximum time to wait for jobs to drain")
	return cmd
}

func runClusterDecommission(cmd *cobra.Command, args []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	name := args[0]
	yes, _ := cmd.Flags().GetBool("yes")
	drain, _ := cmd.Flags().GetBool("drain")
	deadline, _ := cmd.Flags().GetString("deadline")

	if !yes {
		drainNote := ""
		if drain {
			drainNote = fmt.Sprintf(" (all jobs will be drained first, deadline: %s)", deadline)
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"  Decommission cluster %q%s? This will destroy all cluster VMs. [y/N]: ",
			name, drainNote)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	req := map[string]interface{}{
		"Name":     name,
		"Drain":    drain,
		"Deadline": deadline,
	}
	if err := nc.CloudDecommissionCluster(cmd.Context(), name, req); err != nil {
		return fmt.Errorf("decommissioning cluster %q: %w", name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Cluster %q decommission initiated.\n", name)
	return nil
}
