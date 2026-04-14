package compute

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newTerminateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminate <node-id>",
		Short: "Destroy the VM backing a node (requires --cloud)",
		Long: `Terminate destroys the underlying VM for the given Nomad node.
Unlike 'drain', which only removes work from a node, 'terminate' permanently
removes the VM from the cluster. The node should be drained first.`,
		Args: cobra.ExactArgs(1),
		RunE: runNodeTerminate,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("force", false, "Terminate even if the node has running allocations")
	return cmd
}

func runNodeTerminate(cmd *cobra.Command, args []string) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("node terminate requires --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	nc := nomadClientFromCmd(cmd)
	nodeID := args[0]
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")

	if !yes {
		fmt.Fprintf(cmd.OutOrStdout(),
			"  Terminate node %s? This will destroy the underlying VM. [y/N]: ", nodeID)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	req := map[string]interface{}{
		"Force": force,
	}
	if err := nc.CloudTerminateNode(cmd.Context(), nodeID, req); err != nil {
		return fmt.Errorf("terminating node %q: %w", nodeID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Node %s termination initiated.\n", nodeID)
	return nil
}
