package pipeline

import (
	"fmt"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <repository>",
		Short: "Save a pipeline configuration to the cluster",
		Long: `Save a Nextflow pipeline configuration in Nomad Variables so it can be
launched by name from any node in the cluster.

EXAMPLES

  abc pipeline add nextflow-io/hello --name hello
  abc pipeline add https://github.com/nf-core/rnaseq \
      --name rnaseq \
      --revision 3.14.0 \
      --profile test,docker \
      --params-file rnaseq-defaults.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: runAdd,
	}

	cmd.Flags().String("name", "", "Pipeline name (required)")
	cmd.Flags().String("description", "", "Human-readable description")
	cmd.Flags().String("revision", "", "Default git revision")
	cmd.Flags().String("profile", "", "Default Nextflow profile(s), comma-separated")
	cmd.Flags().String("work-dir", "", "Default work directory (default: /work/nextflow-work)")
	cmd.Flags().String("config", "", "Default extra nextflow config file")
	cmd.Flags().String("params-file", "", "Default pipeline parameters (YAML/JSON)")
	cmd.Flags().String("nf-version", "", "Default Nextflow Docker image tag")
	cmd.Flags().String("nf-plugin-version", "", "Default nf-nomad plugin version")
	cmd.Flags().Int("cpu", 0, "Default head job CPU in MHz")
	cmd.Flags().Int("memory", 0, "Default head job memory in MB")
	cmd.Flags().StringSlice("datacenter", nil, "Default Nomad datacenter(s)")

	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func runAdd(cmd *cobra.Command, args []string) error {
	repo := args[0]
	name, _ := cmd.Flags().GetString("name")
	ns := namespaceFromCmd(cmd)

	spec, err := buildSpecFromFlags(cmd, name, repo, ns)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	spec.CreatedAt = now
	spec.UpdatedAt = now

	nc := nomadClientFromCmd(cmd)
	if err := savePipeline(cmd.Context(), nc, spec); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline %q saved.\n", name)
	return nil
}

// buildSpecFromFlags constructs a PipelineSpec from the common add/update flags.
func buildSpecFromFlags(cmd *cobra.Command, name, repo, ns string) (*PipelineSpec, error) {
	spec := &PipelineSpec{
		Name:      name,
		Namespace: ns,
	}
	if repo != "" {
		spec.Repository = repo
	}
	if v, _ := cmd.Flags().GetString("description"); v != "" {
		spec.Description = v
	}
	if v, _ := cmd.Flags().GetString("revision"); v != "" {
		spec.Revision = v
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		spec.Profile = v
	}
	if v, _ := cmd.Flags().GetString("work-dir"); v != "" {
		spec.WorkDir = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		spec.NfVersion = v
	}
	if v, _ := cmd.Flags().GetString("nf-plugin-version"); v != "" {
		spec.NfPluginVersion = v
	}
	if v, _ := cmd.Flags().GetInt("cpu"); v != 0 {
		spec.CPU = v
	}
	if v, _ := cmd.Flags().GetInt("memory"); v != 0 {
		spec.MemoryMB = v
	}
	if v, _ := cmd.Flags().GetStringSlice("datacenter"); len(v) > 0 {
		spec.Datacenters = v
	}
	if configPath, _ := cmd.Flags().GetString("config"); configPath != "" {
		data, err := readFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading --config: %w", err)
		}
		spec.ExtraConfig = string(data)
	}
	if paramsFile, _ := cmd.Flags().GetString("params-file"); paramsFile != "" {
		params, err := utils.LoadParamsFile(paramsFile)
		if err != nil {
			return nil, fmt.Errorf("reading --params-file: %w", err)
		}
		spec.Params = params
	}
	return spec, nil
}
