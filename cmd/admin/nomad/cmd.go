// Package nomad implements the "abc admin services nomad" command group.
//
// This group stays focused on ABC-cluster-specific Nomad operations such as
// namespaces and node lifecycle management. The `cli` subcommand is a
// preconfigured passthrough alias to the local Nomad CLI for operations that
// abc does not yet implement natively.
package nomad

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/namespace"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "nomad" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nomad",
		Short: "ABC-focused Nomad operations plus a passthrough CLI alias",
		Long: `Commands for managing Nomad cluster resources on the ABC-cluster platform.

	abc admin services nomad cli status                                   Run the local Nomad CLI with abc config defaults
  abc admin services nomad namespace list                                 List all namespaces
  abc admin services nomad namespace create --sudo --name=my-lab         Create a namespace
  abc admin services nomad namespace delete --sudo my-lab                Delete a namespace
  abc admin services nomad node drain --sudo nomad-client-02 --wait      Drain a Nomad node
  abc admin services nomad node undrain --sudo nomad-client-02           Restore a Nomad node`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	// Add namespace and node sub-groups
	cmd.AddCommand(namespace.NewCmd())
	cmd.AddCommand(newNodeCmd())
	cmd.AddCommand(newCLICmd())

	return cmd
}

// newNodeCmd returns the "node" subcommand group for Nomad node operations.
func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage Nomad cluster nodes (requires --sudo)",
		Long: `Commands for managing Nomad cluster nodes.

All node operations require --sudo and an admin-tier token.

  abc admin services nomad node drain --sudo nomad-client-02 --deadline=1h --wait
  abc admin services nomad node undrain --sudo nomad-client-02`,
	}

	cmd.AddCommand(
		newDrainCmd(),
		newUndrainCmd(),
	)
	return cmd
}

// nomadClientFromCmd builds a NomadClient honoring sudo mode.
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

// requireSudo returns an error if sudo mode is not active.
func requireSudo(cmd *cobra.Command) error {
	if !utils.SudoFromCmd(cmd) {
		return fmt.Errorf("node operations require --sudo (or ABC_CLI_SUDO_MODE=1)")
	}
	return nil
}
