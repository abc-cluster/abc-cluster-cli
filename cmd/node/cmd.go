// Package node implements the "abc node" command group.
//
// All node operations require --sudo. The X-ABC-Sudo header is forwarded
// to jurist, which enforces the caller's actual permission tier.
package node

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "node" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Inspect and manage cluster nodes (requires --sudo)",
		Long: `Commands for inspecting and managing compute nodes on the ABC-cluster platform.

All node operations require --sudo and an admin-tier token.

  abc node list --sudo
  abc node show --sudo nomad-client-02
  abc node drain --sudo nomad-client-02 --deadline=1h --wait
  abc node undrain --sudo nomad-client-02`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(
		newListCmd(),
		newShowCmd(),
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
	return utils.NewNomadClient(addr, token, region).WithSudo(utils.SudoFromCmd(cmd))
}

// requireSudo returns an error if sudo mode is not active.
func requireSudo(cmd *cobra.Command) error {
	if !utils.SudoFromCmd(cmd) {
		return fmt.Errorf("node operations require --sudo (or ABC_CLI_SUDO_MODE=1)")
	}
	return nil
}
