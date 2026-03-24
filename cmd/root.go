package cmd

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/spf13/cobra"
)

// rootCmd is the base command for the abc CLI.
var rootCmd = &cobra.Command{
	Use:   "abc",
	Short: "abc-cluster CLI",
	Long: `abc is the command line interface for the abc-cluster platform.

It allows you to manage and run batch jobs on the abc-cluster platform
from your terminal.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(job.NewCmd())
}
