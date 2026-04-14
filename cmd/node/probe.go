package node

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newProbeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "probe <node-id>",
		Short: "Test connectivity and readiness of a cluster node",
		Long: `Probe a compute node to verify SSH reachability, Nomad registration,
and driver health. Reports a pass/fail for each check.

  abc infra node probe nomad-client-02`,
		Args: cobra.ExactArgs(1),
		RunE: runProbe,
	}
	cmd.Flags().Bool("ssh", false, "Test SSH connectivity (requires node SSH config)")
	cmd.Flags().Bool("drivers", false, "Test Nomad task driver availability on the node")
	return cmd
}

func runProbe(_ *cobra.Command, args []string) error {
	nodeID := args[0]
	fmt.Printf("  abc infra node probe %q: not yet implemented.\n", nodeID)
	return nil
}
