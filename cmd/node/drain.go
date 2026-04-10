package node

import (
	"fmt"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newDrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drain <node-id>",
		Short: "Enable drain on a node (requires --sudo)",
		Args:  cobra.ExactArgs(1),
		RunE:  runNodeDrain,
	}
	cmd.Flags().String("deadline", "", "Maximum time to wait for allocations to migrate (e.g. 1h, 30m)")
	cmd.Flags().Bool("wait", false, "Block until drain completes")
	return cmd
}

func runNodeDrain(cmd *cobra.Command, args []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	nodeID := args[0]
	deadlineStr, _ := cmd.Flags().GetString("deadline")
	wait, _ := cmd.Flags().GetBool("wait")

	deadlineSecs := 0
	if deadlineStr != "" {
		d, err := time.ParseDuration(deadlineStr)
		if err != nil {
			return fmt.Errorf("invalid --deadline %q: %w", deadlineStr, err)
		}
		deadlineSecs = int(d.Seconds())
	}

	if err := nc.DrainNode(cmd.Context(), nodeID, true, deadlineSecs); err != nil {
		return fmt.Errorf("enabling drain on %q: %w", nodeID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Drain enabled on node %s\n", nodeID)

	if !wait {
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), "  Waiting for drain to complete...")
	for {
		if cmd.Context().Err() != nil {
			return nil
		}
		n, err := nc.GetNode(cmd.Context(), nodeID)
		if err != nil {
			return fmt.Errorf("polling node status: %w", err)
		}
		if !n.Drain {
			fmt.Fprintf(cmd.OutOrStdout(), "  Node %s drain complete.\n", nodeID)
			return nil
		}
		select {
		case <-cmd.Context().Done():
			return nil
		case <-utils.SleepCh(5):
		}
	}
}
