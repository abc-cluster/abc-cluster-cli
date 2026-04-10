// Package budget implements the "abc budget" command group.
//
// Read operations work for all users. Write operations (set) require --cloud.
package budget

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "budget" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "View and manage namespace spend budgets",
		Long: `Commands for viewing and managing cloud spend budgets per namespace.

Reading budgets is available to all users.
Setting budget caps requires --cloud and an infrastructure-tier token.

  abc budget list --cloud
  abc budget show --cloud --namespace=nf-genomics-lab
  abc budget set --cloud --namespace=nf-genomics-lab --monthly=500`,
	}

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
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

func requireCloud(cmd *cobra.Command) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("budget write operations require --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	return nil
}
