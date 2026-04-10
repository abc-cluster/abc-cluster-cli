package node

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Provision and register a new compute node (requires --cloud)",
		RunE:  runNodeAdd,
	}
	cmd.Flags().String("cluster", "", "Target cluster name (or set --cluster / ABC_CLUSTER)")
	cmd.Flags().String("type", "", "VM instance type (e.g. n2-standard-8, g4dn.xlarge)")
	cmd.Flags().String("datacenter", "", "Datacenter label to assign the node to")
	cmd.Flags().Int("count", 1, "Number of nodes to provision")
	cmd.Flags().Bool("dry-run", false, "Print the provisioning plan without creating VMs")
	return cmd
}

func runNodeAdd(cmd *cobra.Command, _ []string) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("node add requires --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	nc := nomadClientFromCmd(cmd)

	cluster := utils.ClusterFromCmd(cmd)
	if v, _ := cmd.Flags().GetString("cluster"); v != "" {
		cluster = v
	}
	nodeType, _ := cmd.Flags().GetString("type")
	datacenter, _ := cmd.Flags().GetString("datacenter")
	count, _ := cmd.Flags().GetInt("count")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	req := map[string]interface{}{
		"Cluster":    cluster,
		"NodeType":   nodeType,
		"Datacenter": datacenter,
		"Count":      count,
		"DryRun":     dryRun,
	}

	var resp map[string]interface{}
	if err := nc.CloudAddNode(cmd.Context(), req, &resp); err != nil {
		return fmt.Errorf("provisioning node: %w", err)
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  Dry-run: %d %s node(s) would be added to cluster %q.\n",
			count, nodeType, cluster)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Node provisioning started (%d x %s).\n", count, nodeType)
	return nil
}
