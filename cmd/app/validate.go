package app

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate abc-app.yaml and print the resolved values",
		Long: `Validate an abc-app.yaml against the schema + phase-1 rules and print the
resolved (post-defaults) values. Does not contact the cluster — for the
templated Nomad HCL, use 'abc app deploy --dry-run'.`,
		Args: cobra.NoArgs,
		RunE: runValidate,
	}
	cmd.Flags().StringP("file", "f", appgen.DefaultSpecFile, "Path to the app descriptor")
	return cmd
}

func runValidate(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	file, _ := cmd.Flags().GetString("file")

	spec, err := appgen.Load(file)
	if err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("invalid %s: %w", file, err)
	}
	spec.ApplyDefaults()

	fmt.Fprintf(out, "%s is valid. Resolved values:\n", file)
	fmt.Fprint(out, spec.ResolvedSummary())
	return nil
}
