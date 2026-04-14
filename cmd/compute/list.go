package compute

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cluster nodes (requires --sudo)",
		RunE:  runNodeList,
	}
	addStructuredOutputFlags(cmd, outputTable)
	return cmd
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

	return writeStructuredOutput(cmd, nodes, func(out io.Writer) {
		if len(nodes) == 0 {
			fmt.Fprintln(out, "  No nodes found.")
			return
		}

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
	})
}
