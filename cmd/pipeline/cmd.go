// Package pipeline implements the "abc pipeline" command group.
//
// Saved pipelines are stored in Nomad Variables at nomad/pipelines/<name> and
// the head job HCL is generated locally then submitted via the Nomad API —
// no separate ABC API server required.
package pipeline

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "pipeline" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage and run Nextflow pipelines",
		Long: `Commands for saving, launching, and managing Nextflow pipelines on the
ABC-cluster platform.

Saved pipelines are stored in Nomad Variables (nomad/pipelines/<name>) so they
are available cluster-wide. You can launch them by name or supply a pipeline
repository URL directly for ad-hoc runs.

  abc pipeline run nextflow-io/hello --profile hello
  abc pipeline run my-saved-pipeline --params-file custom.yaml
  abc pipeline add https://github.com/nf-core/rnaseq --name rnaseq
  abc pipeline list`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")
	cmd.PersistentFlags().String("namespace", utils.EnvOrDefault("ABC_NAMESPACE", "NOMAD_NAMESPACE"),
		"Nomad namespace for saved pipeline storage (or set ABC_NAMESPACE/NOMAD_NAMESPACE)")

	cmd.AddCommand(
		newRunCmd(),
		newRunsCmd(),
		newAddCmd(),
		newListCmd(),
		newInfoCmd(),
		newUpdateCmd(),
		newDeleteCmd(),
		newExportCmd(),
		newImportCmd(),
		newParamsCmd(),
	)
	return cmd
}

// nomadClientFromCmd builds a NomadClient from the command's persistent flags.
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

// namespaceFromCmd reads the --namespace flag, falling back up to the root.
func namespaceFromCmd(cmd *cobra.Command) string {
	ns, _ := cmd.Flags().GetString("namespace")
	if ns == "" {
		ns, _ = cmd.Root().PersistentFlags().GetString("namespace")
	}
	return ns
}
