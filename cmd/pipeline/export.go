package pipeline

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <name> [output-file]",
		Short: "Export a saved pipeline configuration to YAML",
		Long: `Export a saved pipeline to a YAML file (or stdout if no file is given).
The exported file can be checked into source control or imported into another
cluster with "abc pipeline import".`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runExport,
	}
	return cmd
}

func runExport(cmd *cobra.Command, args []string) error {
	name := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	spec, err := loadPipeline(cmd.Context(), nc, name, ns)
	if err != nil {
		return err
	}
	if spec == nil {
		return fmt.Errorf("pipeline %q not found", name)
	}

	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("serialising pipeline: %w", err)
	}

	if len(args) == 2 {
		outPath := args[1]
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return fmt.Errorf("writing %q: %w", outPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline %q exported to %s\n", name, outPath)
		return nil
	}

	fmt.Fprint(cmd.OutOrStdout(), string(data))
	return nil
}
