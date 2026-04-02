package pipeline

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a pipeline configuration from a YAML file",
		Long: `Import a pipeline from a YAML file previously created with "abc pipeline export".
Use --name to override the pipeline name in the file.`,
		Args: cobra.ExactArgs(1),
		RunE: runImport,
	}
	cmd.Flags().String("name", "", "Override the pipeline name from the file")
	return cmd
}

func runImport(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	data, err := readFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %q: %w", filePath, err)
	}

	var spec PipelineSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("parsing pipeline YAML: %w", err)
	}

	if v, _ := cmd.Flags().GetString("name"); v != "" {
		spec.Name = v
	}
	if spec.Name == "" {
		return fmt.Errorf("pipeline name is required (set 'name' in the YAML or use --name)")
	}

	ns := namespaceFromCmd(cmd)
	if ns != "" {
		spec.Namespace = ns
	}
	now := time.Now().UTC()
	spec.UpdatedAt = now
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = now
	}

	nc := nomadClientFromCmd(cmd)
	if err := savePipeline(cmd.Context(), nc, &spec); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline %q imported.\n", spec.Name)
	return nil
}
