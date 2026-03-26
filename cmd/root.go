package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
	"github.com/spf13/cobra"
)

var (
	// ServerURL is the abc-cluster API endpoint.
	ServerURL string
	// AccessToken is the API access token.
	AccessToken string
	// Workspace is the workspace ID.
	Workspace string
)

// rootCmd is the base command for the abc CLI.
var rootCmd = &cobra.Command{
	Use:          "abc",
	Short:        "abc-cluster CLI",
	SilenceUsage: true,
	SilenceErrors: true,
	Long: `abc is the command line interface for the abc-cluster platform.

It allows you to manage and run pipelines and batch jobs on the abc-cluster platform
from your terminal.`,
}

// Execute runs the root command.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			action := cancelledActionFromArgs(os.Args[1:])
			if action == "" {
				fmt.Fprintln(os.Stderr, "cancelled")
			} else {
				fmt.Fprintf(os.Stderr, "cancelled: %s\n", action)
			}
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cancelledActionFromArgs(args []string) string {
	actionParts := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		actionParts = append(actionParts, arg)
	}
	if len(actionParts) == 0 {
		return ""
	}
	if len(actionParts) > 2 {
		actionParts = actionParts[:2]
	}
	return strings.Join(actionParts, " ")
}

func init() {
	rootCmd.PersistentFlags().StringVar(
		&ServerURL, "url",
		getEnvOrDefault("ABC_API_ENDPOINT", "https://api.abc-cluster.io"),
		"abc-cluster API endpoint URL (or set ABC_API_ENDPOINT)",
	)
	rootCmd.PersistentFlags().StringVar(
		&AccessToken, "access-token",
		os.Getenv("ABC_ACCESS_TOKEN"),
		"abc-cluster access token (or set ABC_ACCESS_TOKEN)",
	)
	rootCmd.PersistentFlags().StringVar(
		&Workspace, "workspace",
		os.Getenv("ABC_WORKSPACE_ID"),
		"workspace ID (or set ABC_WORKSPACE_ID)",
	)

	rootCmd.AddCommand(pipeline.NewCmd(&ServerURL, &AccessToken, &Workspace))
	rootCmd.AddCommand(data.NewCmd(&ServerURL, &AccessToken, &Workspace))
	rootCmd.AddCommand(job.NewCmd())
}

// getEnvOrDefault returns the value of the environment variable or the given default.
func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
