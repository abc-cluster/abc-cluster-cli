package pipeline

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update the default configuration of a saved pipeline",
		Long: `Update one or more fields of a saved pipeline. Only flags that are
explicitly provided will be changed; omitted flags leave the existing value
intact.

EXAMPLE

  abc pipeline update rnaseq --revision 3.15.0 --params-file new-defaults.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: runUpdate,
	}

	cmd.Flags().String("description", "", "New description")
	cmd.Flags().String("revision", "", "New default revision")
	cmd.Flags().String("profile", "", "New default profile(s)")
	cmd.Flags().String("work-dir", "", "New default work directory")
	cmd.Flags().String("config", "", "New default extra nextflow config file")
	cmd.Flags().String("params-file", "", "New default parameters (YAML/JSON) — replaces existing")
	cmd.Flags().String("nf-version", "", "New Nextflow Docker image tag")
	cmd.Flags().String("nf-plugin-version", "", "New nf-nomad plugin version")
	cmd.Flags().Int("cpu", 0, "New head job CPU in MHz")
	cmd.Flags().Int("memory", 0, "New head job memory in MB")
	cmd.Flags().StringSlice("datacenter", nil, "New default Nomad datacenter(s)")

	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	existing, err := loadPipeline(cmd.Context(), nc, name, ns)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("pipeline %q not found — use 'abc pipeline add' to create it", name)
	}

	override, err := buildSpecFromFlags(cmd, name, "", ns)
	if err != nil {
		return err
	}
	merged := mergeSpec(existing, override)
	merged.UpdatedAt = time.Now().UTC()

	if err := savePipeline(cmd.Context(), nc, merged); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline %q updated.\n", name)
	return nil
}
