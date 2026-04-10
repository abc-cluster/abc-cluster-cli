package cluster

import (
	"fmt"
	"sort"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// ClusterDetail is a full cluster description from the cloud gateway.
type ClusterDetail struct {
	Name         string            `json:"Name"`
	Region       string            `json:"Region"`
	Status       string            `json:"Status"`
	NodeCount    int               `json:"NodeCount"`
	NomadVersion string            `json:"NomadVersion"`
	Datacenters  []string          `json:"Datacenters"`
	Meta         map[string]string `json:"Meta"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show cluster status (requires --cloud)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runClusterStatus,
	}
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	name := utils.ClusterFromCmd(cmd)
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("specify a cluster name as argument or via --cluster / ABC_CLUSTER")
	}

	var detail ClusterDetail
	if err := nc.CloudGetCluster(cmd.Context(), name, &detail); err != nil {
		return fmt.Errorf("fetching cluster %q: %w", name, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Name         %s\n", detail.Name)
	fmt.Fprintf(out, "  Region       %s\n", detail.Region)
	fmt.Fprintf(out, "  Status       %s\n", detail.Status)
	fmt.Fprintf(out, "  Nodes        %d\n", detail.NodeCount)
	fmt.Fprintf(out, "  Nomad        %s\n", detail.NomadVersion)
	if len(detail.Datacenters) > 0 {
		fmt.Fprintf(out, "  Datacenters  %v\n", detail.Datacenters)
	}
	if len(detail.Meta) > 0 {
		fmt.Fprintf(out, "\n  Metadata:\n")
		keys := make([]string, 0, len(detail.Meta))
		for k := range detail.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "    %-16s %s\n", k, detail.Meta[k])
		}
	}
	return nil
}
