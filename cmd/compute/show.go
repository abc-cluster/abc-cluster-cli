package compute

import (
	"fmt"
	"io"
	"sort"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <node-id>",
		Short: "Show node details (requires --sudo)",
		Args:  cobra.ExactArgs(1),
		RunE:  runNodeShow,
	}
	addStructuredOutputFlags(cmd, outputTable)
	return cmd
}

type nodeShowPayload struct {
	Node              *utils.NomadNode       `json:"node"`
	ActiveAllocations []utils.NomadAllocStub `json:"active_allocations"`
}

func runNodeShow(cmd *cobra.Command, args []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	nodeID := args[0]

	n, err := nc.GetNode(cmd.Context(), nodeID)
	if err != nil {
		return fmt.Errorf("fetching node %q: %w", nodeID, err)
	}

	activeAllocs := make([]utils.NomadAllocStub, 0, 8)
	allocs, err := nc.GetNodeAllocs(cmd.Context(), nodeID)
	if err == nil && len(allocs) > 0 {
		for _, a := range allocs {
			if a.ClientStatus == "complete" || a.DesiredStatus == "stop" {
				continue
			}
			activeAllocs = append(activeAllocs, a)
		}
	}

	payload := nodeShowPayload{Node: n, ActiveAllocations: activeAllocs}
	return writeStructuredOutput(cmd, payload, func(out io.Writer) {
		fmt.Fprintf(out, "  ID          %s\n", n.ID)
		fmt.Fprintf(out, "  Name        %s\n", n.Name)
		fmt.Fprintf(out, "  Datacenter  %s\n", n.Datacenter)
		fmt.Fprintf(out, "  Region      %s\n", n.Region)
		fmt.Fprintf(out, "  Class       %s\n", n.NodeClass)
		fmt.Fprintf(out, "  Status      %s\n", n.Status)
		drain := "no"
		if n.Drain {
			drain = "yes"
		}
		fmt.Fprintf(out, "  Drain       %s\n", drain)
		elig := n.SchedulingEligibility
		if elig == "" {
			elig = "eligible"
		}
		fmt.Fprintf(out, "  Eligibility %s\n", elig)

		if n.NodeResources != nil && (n.NodeResources.CPU > 0 || n.NodeResources.MemoryMB > 0) {
			fmt.Fprintf(out, "\n  Resources:\n")
			fmt.Fprintf(out, "    CPU       %d MHz\n", n.NodeResources.CPU)
			fmt.Fprintf(out, "    Memory    %d MiB\n", n.NodeResources.MemoryMB)
			fmt.Fprintf(out, "    Disk      %d MiB\n", n.NodeResources.DiskMB)
		}

		if len(n.Drivers) > 0 {
			fmt.Fprintf(out, "\n  Drivers:\n")
			driverNames := make([]string, 0, len(n.Drivers))
			for d := range n.Drivers {
				driverNames = append(driverNames, d)
			}
			sort.Strings(driverNames)
			for _, d := range driverNames {
				info := n.Drivers[d]
				status := "healthy"
				if !info.Healthy {
					status = "unhealthy"
				}
				if !info.Detected {
					status = "not detected"
				}
				fmt.Fprintf(out, "    %-16s %s\n", d, status)
			}
		}

		if len(activeAllocs) > 0 {
			fmt.Fprintf(out, "\n  Allocations:\n")
			for _, a := range activeAllocs {
				fmt.Fprintf(out, "    %-38s %-20s %s\n", a.ID[:min(8, len(a.ID))], a.JobID, a.ClientStatus)
			}
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
