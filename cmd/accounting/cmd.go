// Package accounting implements the "abc accounting" command group (cloud spend /
// allocation caps via the cloud gateway). Aliases: cost, budget.
//
// Read operations require --cloud. Write operations (set) require --cloud and
// appropriate gateway policy.
package accounting

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "accounting" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounting",
		Aliases: []string{"cost", "budget"},
		Short:   "Accounting: cloud spend and namespace budget caps",
		Long: `View and manage cloud accounting data (namespace spend caps via the cloud gateway).

Subcommands focus on per-namespace budgets (monthly caps, alerts). Use --budget on the
parent command to pass an optional allocation / budget id when supported by the gateway.

  abc --cloud accounting list
  abc --cloud accounting show --namespace=nf-genomics-lab
  abc --cloud accounting set --namespace=nf-genomics-lab --monthly=500

Legacy: abc cost … and abc budget … are aliases for abc accounting …`,
	}

	cmd.PersistentFlags().String("budget", "",
		"optional budget / allocation id (for future filters; reserved when gateway supports it)")
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Cloud gateway address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Auth token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(
		newListCmd(),
		newShowCmd(),
		newSetCmd(),
	)
	return cmd
}

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

func requireCloud(cmd *cobra.Command) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("accounting commands require --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	return nil
}
