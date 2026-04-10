package namespace

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all namespaces",
		RunE:  runNsList,
	}
}

func runNsList(cmd *cobra.Command, _ []string) error {
	nc := nomadClientFromCmd(cmd)

	namespaces, err := nc.ListNamespaces(cmd.Context())
	if err != nil {
		return fmt.Errorf("listing namespaces: %w", err)
	}

	if len(namespaces) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No namespaces found.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-24s %-16s %-16s %-30s\n", "NAME", "GROUP", "CONTACT", "DESCRIPTION")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 90))
	for _, ns := range namespaces {
		group := ns.Meta["group"]
		contact := ns.Meta["contact"]
		desc := ns.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}
		fmt.Fprintf(out, "  %-24s %-16s %-16s %-30s\n", ns.Name, group, contact, desc)
	}
	return nil
}
