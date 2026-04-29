package job

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <job-id>",
		Short: "Stop a running Nomad batch job",
		Args:  cobra.ExactArgs(1),
		RunE:  runStop,
	}
	cmd.Flags().Bool("purge", false, "Remove job definition from Nomad after stopping")
	cmd.Flags().Bool("detach", false, "Return immediately without waiting")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	return cmd
}

func runStop(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	purge, _ := cmd.Flags().GetBool("purge")
	yes, _ := cmd.Flags().GetBool("yes")
	ns := namespaceFromCmd(cmd)

	if !yes {
		purgeNote := ""
		if purge {
			purgeNote = " (job definition will also be purged)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Stop job %s%s? [y/N]: ", jobID, purgeNote)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	nc := nomadClientFromCmd(cmd)
	resp, err := nc.StopJob(cmd.Context(), jobID, ns, purge)
	if err != nil {
		return fmt.Errorf("stopping job %q: %w", jobID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  ✓ Stop signal sent\n")
	if resp.EvalID != "" {
		fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	}
	if purge {
		fmt.Fprintf(out, "  ✓ Job definition purged from Nomad\n")
	}
	return nil
}
