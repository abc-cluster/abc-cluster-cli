package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <name>",
		Short: "Show details of a saved pipeline",
		Args:  cobra.ExactArgs(1),
		RunE:  runInfo,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runInfo(cmd *cobra.Command, args []string) error {
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

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		data, _ := json.MarshalIndent(spec, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}

	out := cmd.OutOrStdout()
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(out, "  %-20s %s\n", label, value)
		}
	}
	fmt.Fprintf(out, "\n  Pipeline: %s\n", spec.Name)
	fmt.Fprintln(out, "  "+strings.Repeat("─", 50))
	row("Repository:", spec.Repository)
	row("Description:", spec.Description)
	row("Revision:", spec.Revision)
	row("Profile:", spec.Profile)
	row("Work dir:", spec.WorkDir)
	row("Namespace:", spec.Namespace)
	if len(spec.Datacenters) > 0 {
		row("Datacenters:", strings.Join(spec.Datacenters, ", "))
	}
	if spec.CPU > 0 {
		row("CPU (MHz):", fmt.Sprintf("%d", spec.CPU))
	}
	if spec.MemoryMB > 0 {
		row("Memory (MB):", fmt.Sprintf("%d", spec.MemoryMB))
	}
	row("NF version:", spec.NfVersion)
	row("NF plugin:", spec.NfPluginVersion)
	if len(spec.Params) > 0 {
		row("Params:", fmt.Sprintf("%d key(s)", len(spec.Params)))
	}
	if spec.ExtraConfig != "" {
		row("Extra config:", "(present)")
	}
	if !spec.CreatedAt.IsZero() {
		row("Created:", spec.CreatedAt.Format("2006-01-02 15:04 UTC"))
	}
	if !spec.UpdatedAt.IsZero() {
		row("Updated:", spec.UpdatedAt.Format("2006-01-02 15:04 UTC"))
	}
	fmt.Fprintln(out)
	return nil
}
