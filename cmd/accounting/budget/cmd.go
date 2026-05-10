// Package budget implements the "abc accounting budget" subgroup —
// namespace spend caps and admission-gate thresholds, managed via the
// cloud gateway. Available at grove+ and cloud tiers; rejected at
// seedling via the capability layer.
package budget

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "budget" subgroup under "accounting".
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Manage namespace budget caps and admission-gate thresholds",
		Long: `Manage per-namespace monthly spend caps and admission-gate thresholds.

  abc --cloud accounting budget list
  abc --cloud accounting budget show --namespace=nf-genomics-lab
  abc --cloud accounting budget set --namespace=nf-genomics-lab --monthly=500

Available at grove+ and cloud tiers (requires abc-controller-svc and
abc-policy-svc). At seedling, these verbs reject with the standard
capability message.`,
	}

	cmd.AddCommand(
		newListCmd(),
		newShowCmd(),
		newSetCmd(),
	)
	return cmd
}

// nomadClientFromCmd builds a Nomad client from the command's persistent
// flags (defined on the parent `accounting` command), falling back to
// config defaults.
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

// requireCloud rejects when not invoked with --cloud (or
// ABC_CLI_CLOUD_MODE=1). Budget management flows through the cloud
// gateway today; the capability-layer rejection at seedling is a
// separate gate added in commit 3 of the verb-tree-restructure spec.
func requireCloud(cmd *cobra.Command) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("accounting budget commands require --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	return nil
}
