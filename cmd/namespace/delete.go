package namespace

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
		Short: "Delete a namespace (requires --sudo)",
		Args:  cobra.ExactArgs(1),
		RunE:  runNsDelete,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("drain", false, "Stop all running jobs in the namespace before deletion")
	return cmd
}

func runNsDelete(cmd *cobra.Command, args []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	name := args[0]
	yes, _ := cmd.Flags().GetBool("yes")
	drain, _ := cmd.Flags().GetBool("drain")

	if !yes {
		drainNote := ""
		if drain {
			drainNote = " (all running jobs will be stopped first)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Delete namespace %q%s? [y/N]: ", name, drainNote)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	if drain {
		jobs, err := nc.ListJobs(cmd.Context(), "", name)
		if err != nil {
			return fmt.Errorf("listing jobs in namespace %q: %w", name, err)
		}
		for _, j := range jobs {
			if j.Status == "running" || j.Status == "pending" {
				if _, err := nc.StopJob(cmd.Context(), j.ID, name, false); err != nil {
					return fmt.Errorf("stopping job %q: %w", j.ID, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Stopped job %s\n", j.ID)
			}
		}
	}

	if err := nc.DeleteNamespace(cmd.Context(), name); err != nil {
		return fmt.Errorf("deleting namespace %q: %w", name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Namespace %q deleted.\n", name)
	return nil
}
