package namespace

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show namespace details",
		Args:  cobra.ExactArgs(1),
		RunE:  runNsShow,
	}
}

func runNsShow(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	name := args[0]

	ns, err := nc.GetNamespace(cmd.Context(), name)
	if err != nil {
		return fmt.Errorf("fetching namespace %q: %w", name, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Name         %s\n", ns.Name)
	if ns.Description != "" {
		fmt.Fprintf(out, "  Description  %s\n", ns.Description)
	}

	if len(ns.Meta) > 0 {
		fmt.Fprintf(out, "\n  Metadata:\n")
		keys := make([]string, 0, len(ns.Meta))
		for k := range ns.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "    %-16s %s\n", k, ns.Meta[k])
		}
	}
	return nil
}
