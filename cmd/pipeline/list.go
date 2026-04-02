package pipeline

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved pipelines",
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, _ []string) error {
	nc := nomadClientFromCmd(cmd)
	ns := namespaceFromCmd(cmd)

	stubs, err := listPipelines(cmd.Context(), nc, ns)
	if err != nil {
		return err
	}

	if len(stubs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No saved pipelines found.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-30s %-20s\n", "NAME", "LAST UPDATED")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 52))
	for _, s := range stubs {
		updated := "—"
		if !s.ModifyTime.IsZero() {
			updated = s.ModifyTime.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(out, "  %-30s %-20s\n", s.Name, updated)
	}
	return nil
}
