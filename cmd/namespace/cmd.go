// Package namespace implements the "abc namespace" command group.
//
// Without --sudo, namespace commands are read-only (list, show).
// With --sudo, write operations are available (create, delete).
// The X-ABC-Sudo header is forwarded to jurist, which enforces
// the caller's actual permission tier server-side.
package namespace

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "namespace" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "namespace",
		Short: "Manage cluster namespaces",
		Long: `Commands for listing and managing Nomad namespaces on the ABC-cluster platform.

Read operations (list, show) are available to all users.
Write operations (create, delete) require --sudo and an admin-tier token.

  abc namespace list --sudo
  abc namespace show nf-genomics-lab
  abc namespace create --sudo --name=nf-new-lab --contact=pi@lab.edu
  abc namespace delete --sudo nf-old-lab`,
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
		newCreateCmd(),
		newDeleteCmd(),
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
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

// requireSudo returns an error if sudo mode is not active. Used as a
// pre-check for write operations before sending to jurist.
func requireSudo(cmd *cobra.Command) error {
	if !utils.SudoFromCmd(cmd) {
		return fmt.Errorf("this operation requires --sudo (or ABC_CLI_SUDO_MODE=1)")
	}
	return nil
}
