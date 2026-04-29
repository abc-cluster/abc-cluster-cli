package module

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/module/samplesheet"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "module" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: "Generate and run nf-core module driver pipelines",
		Long: `Commands for generating module driver pipelines (via nf-pipeline-gen)
and running them on Nomad as batch jobs.

Examples:
  abc module run nf-core/fastqc
  abc module run nf-core/cat/fastq --wait --logs
  abc module run nf-core/fastqc --params-file params.yml --config-file module.config
  abc module run nf-core/fastqc --params-file params.yml --config-file module.config --pipeline-gen-no-run-manifest`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")
	cmd.PersistentFlags().String("namespace", utils.EnvOrDefault("ABC_NAMESPACE", "NOMAD_NAMESPACE"),
		"Nomad namespace for job submission (or set ABC_NAMESPACE/NOMAD_NAMESPACE)")

	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(samplesheet.NewCmd())
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
