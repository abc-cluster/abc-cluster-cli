package compute

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cluster nodes (requires --sudo)",
		RunE:  runNodeList,
	}
}

func runNodeList(cmd *cobra.Command, _ []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	nodes, err := nc.ListNodes(cmd.Context())
	if err != nil {
		return fmt.Errorf("listing nodes: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No nodes found.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-10s %-20s %-14s %-12s %-8s %-12s\n",
		"ID", "NAME", "DATACENTER", "STATUS", "DRAIN", "ELIGIBILITY")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 84))

	for _, n := range nodes {
		id := n.ID
		if len(id) > 8 {
			id = id[:8]
		}
		drain := "no"
		if n.Drain {
			drain = "yes"
		}
		elig := n.SchedulingEligibility
		if elig == "" {
			elig = "eligible"
		}
		fmt.Fprintf(out, "  %-10s %-20s %-14s %-12s %-8s %-12s\n",
			id, n.Name, n.Datacenter, n.Status, drain, elig)
	}
	return nil
}
