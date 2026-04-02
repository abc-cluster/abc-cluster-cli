package pipeline

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a saved pipeline from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE:  runDelete,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	yes, _ := cmd.Flags().GetBool("yes")

	if !yes {
		fmt.Fprintf(cmd.OutOrStdout(), "  Delete saved pipeline %q? [y/N]: ", name)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	if err := deletePipeline(cmd.Context(), nc, name, ns); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline %q deleted.\n", name)
	return nil
}
