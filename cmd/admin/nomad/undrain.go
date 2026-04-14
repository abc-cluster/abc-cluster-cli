package nomad

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newUndrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undrain <node-id>",
		Short: "Disable drain and restore eligibility on a node (requires --sudo)",
		Args:  cobra.ExactArgs(1),
		RunE:  runNodeUndrain,
	}
}

func runNodeUndrain(cmd *cobra.Command, args []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	nodeID := args[0]

	if err := nc.DrainNode(cmd.Context(), nodeID, false, 0); err != nil {
		return fmt.Errorf("disabling drain on %q: %w", nodeID, err)
	}
	if err := nc.SetNodeEligibility(cmd.Context(), nodeID, true); err != nil {
		return fmt.Errorf("restoring eligibility on %q: %w", nodeID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Node %s drain disabled and marked eligible.\n", nodeID)
	return nil
}
