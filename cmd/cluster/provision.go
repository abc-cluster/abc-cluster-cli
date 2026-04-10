package cluster

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newProvisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Provision a new cluster (requires --cloud)",
		RunE:  runClusterProvision,
	}
	cmd.Flags().String("name", "", "Cluster name (required)")
	cmd.Flags().String("region", "", "Cloud region for the cluster (required)")
	cmd.Flags().Int("size", 3, "Number of client nodes")
	cmd.Flags().String("node-type", "", "VM instance type for client nodes")
	cmd.Flags().String("nomad-version", "", "Nomad version to install (default: latest)")
	cmd.Flags().Bool("dry-run", false, "Print the provisioning plan without creating resources")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("region")
	return cmd
}

func runClusterProvision(cmd *cobra.Command, _ []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	name, _ := cmd.Flags().GetString("name")
	region, _ := cmd.Flags().GetString("region")
	size, _ := cmd.Flags().GetInt("size")
	nodeType, _ := cmd.Flags().GetString("node-type")
	nomadVersion, _ := cmd.Flags().GetString("nomad-version")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	req := map[string]interface{}{
		"Name":         name,
		"Region":       region,
		"NodeCount":    size,
		"NodeType":     nodeType,
		"NomadVersion": nomadVersion,
		"DryRun":       dryRun,
	}

	var resp map[string]interface{}
	if err := nc.CloudProvisionCluster(cmd.Context(), req, &resp); err != nil {
		return fmt.Errorf("provisioning cluster %q: %w", name, err)
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  Dry-run: cluster %q would be provisioned in %s with %d node(s).\n",
			name, region, size)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "  Cluster %q provisioning started in %s.\n", name, region)
	if id, ok := resp["ID"].(string); ok && id != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Provisioning ID  %s\n", id)
	}
	return nil
}
