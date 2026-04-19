// Package cluster implements the "abc cluster" command group.
//
// All cluster operations require --cloud. Requests carry X-ABC-Cloud: 1 to
// the cloud gateway, which handles multi-cluster fleet operations and cloud
// provider API calls. The CLI generates intent; the gateway enforces policy.
package cluster

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "cluster" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage the cluster fleet (requires --cloud)",
		Long: `Commands for listing and managing Nomad clusters in the fleet.

All cluster operations require --cloud and an infrastructure-tier token.

  abc cluster list --cloud
  abc cluster status --cloud --cluster=za-cpt
  abc cluster provision --cloud --name=nf-genomics-gpu --region=eu-west --size=5
  abc cluster decommission --cloud nf-old-cluster`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Cloud gateway address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Auth token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Default region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newProvisionCmd(),
		newDecommissionCmd(),
		newCapabilitiesCmd(),
	)
	return cmd
}

// nomadClientFromCmd builds a NomadClient with cloud mode wired.
func nomadClientFromCmd(cmd *cobra.Command) *utils.NomadClient {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ := cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	if addr == "" || token == "" || region == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

// requireCloud returns an error if cloud mode is not active.
func requireCloud(cmd *cobra.Command) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("cluster operations require --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	return nil
}
