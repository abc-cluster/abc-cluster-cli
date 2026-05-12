// Package compute implements the "abc infra compute" command group.
//
// All compute operations require --sudo. The X-ABC-Sudo header is forwarded
// to jurist, which enforces the caller's actual permission tier.
//
// DEFERRED CAPABILITY NOTICE (2026-05-11):
// The mutating verbs in this package — specifically `abc infra compute add`
// (worker-node onboarding) and any future `abc infra compute promote`
// (topology promotion) — are NOT part of the shipped product surface for
// the abc-seedling tier today. Seedling infrastructure is provisioned by
// the separate abc-deployments project (Pulumi-driven): `pulumi up`
// against a stack configuration installs Nomad, MinIO, Tailscale, the
// observability stack, and the seedling-tended hygiene configuration.
// The CLI's seedling-tier role is observational only — abc cluster status,
// abc cluster capabilities show, abc cluster doctor, abc auth context
// list, abc data ls, abc pipeline list.
//
// The scaffolding here is retained for a future capability: at the
// abc-cloud (cloud) tier, an operator who has provisioned their own VMs
// in their own cloud account will invoke `abc infra compute add` against
// an abc-cloud-managed control plane to register those VMs as Nomad
// workers without operating their own deployment codebase. That feature
// is forward-looking and is documented as such in the cloud-bridge
// brainstorm; it is not part of the abc-seedling or abc-grove tier
// surfaces today.
//
// Read-only verbs in this package (`list`, `show`, `probe`, `node debug`)
// remain in scope and operational at all tiers — they are observability
// surfaces, not provisioning surfaces.
//
// See: design/decided/abc-seedling-scope.md §1a (CLI scope vs deployment
// scope); manuscripts/abc-seedling-manuscript-anchor.md §1 pillar 3;
// brainstorms/cloud-bridge/ for the future cloud-node-onboarding design.
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

Most operations require --sudo and an admin-tier token (Jurist). The exception is
  abc infra compute node debug
which uses SSH or the local shell only (no --sudo on the abc command).

  abc infra compute list --sudo
  abc infra compute show --sudo nomad-client-02
  abc infra compute add nomad-client-03 --remote=10.0.0.5
  abc infra compute node debug --remote=sun-aither
  abc infra compute terminate --sudo nomad-client-02`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("NOMAD_ADDR"),
		"Nomad API address (or set NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("NOMAD_TOKEN"),
		"Nomad ACL token (or set NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("NOMAD_REGION"),
		"Nomad region (or set NOMAD_REGION)")

	cmd.AddCommand(
		newListCmd(),
		newShowCmd(),
		newAddCmd(),
		newTerminateCmd(),
		newProbeCmd(),
		newProbeScheduleCmd(),
		newNodeCmd(),
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
		return fmt.Errorf("node operations require --sudo (or ABC_CLI_SUDO=1)")
	}
	return nil
}
