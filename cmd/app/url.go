package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newURLCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "url <name>",
		Short: "Print an app's URL (pipe-friendly)",
		Args:  cobra.ExactArgs(1),
		RunE:  runURL,
	}
}

func runURL(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	r, err := resolveApp(cmd.Context(), nc, args[0])
	if err != nil {
		return err
	}
	// No decoration — just the URL, suitable for piping (e.g. | pbcopy).
	fmt.Fprintln(cmd.OutOrStdout(), appURLFromJob(r))
	return nil
}
