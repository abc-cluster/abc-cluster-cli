// Package compute implements the "abc infra compute" command group.
//
// All compute operations require --sudo. The X-ABC-Sudo header is forwarded
// to jurist, which enforces the caller's actual permission tier.
package compute

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "compute" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Inspect and manage cluster compute resources (requires --sudo)",
		Long: `Commands for inspecting and managing compute resources on the ABC-cluster platform.

All compute operations require --sudo and an admin-tier token.

  abc infra compute list --sudo
  abc infra compute show --sudo nomad-client-02
  abc infra compute add nomad-client-03 --remote=10.0.0.5
  abc infra compute terminate --sudo nomad-client-02`,
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
		newAddCmd(),
		newTerminateCmd(),
		newProbeCmd(),
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
