package cluster

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ClusterStub is a summary of a managed Nomad cluster returned by the cloud gateway.
type ClusterStub struct {
	Name       string `json:"Name"`
	Region     string `json:"Region"`
	Status     string `json:"Status"`
	NodeCount  int    `json:"NodeCount"`
	NomadVersion string `json:"NomadVersion"`
	CreateTime int64  `json:"CreateTime"`
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all clusters in the fleet (requires --cloud)",
		RunE:  runClusterList,
	}
}

func runClusterList(cmd *cobra.Command, _ []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	var clusters []ClusterStub
	if err := nc.CloudListClusters(cmd.Context(), &clusters); err != nil {
		return fmt.Errorf("listing clusters: %w", err)
	}

	if len(clusters) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No clusters found.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-24s %-12s %-10s %-6s %-12s %-18s\n",
		"NAME", "REGION", "STATUS", "NODES", "NOMAD", "CREATED")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 90))
	for _, c := range clusters {
		created := "—"
		if c.CreateTime > 0 {
			created = time.Unix(0, c.CreateTime).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(out, "  %-24s %-12s %-10s %-6d %-12s %-18s\n",
			c.Name, c.Region, c.Status, c.NodeCount, c.NomadVersion, created)
	}
	return nil
}
