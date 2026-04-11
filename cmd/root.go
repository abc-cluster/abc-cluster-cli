package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/abc-cluster/abc-cluster-cli/cmd/budget"
	"github.com/abc-cluster/abc-cluster-cli/cmd/cluster"
	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/abc-cluster/abc-cluster-cli/cmd/namespace"
	"github.com/abc-cluster/abc-cluster-cli/cmd/node"
	"github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
	"github.com/abc-cluster/abc-cluster-cli/cmd/service"
	"github.com/abc-cluster/abc-cluster-cli/cmd/storage"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// serverURL / accessToken / workspace are used by the data command which
// still targets the ABC REST API.
var (
	serverURL   string
	accessToken string
	workspace   string
)

// rootCmd is the base command for the abc CLI.
var rootCmd = &cobra.Command{
	Use:           "abc",
	Short:         "abc-cluster CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
	Long: `abc is the command line interface for the abc-cluster platform.

It allows you to manage and run pipelines and batch jobs on the abc-cluster platform
from your terminal.`,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
		if utils.CloudFromCmd(cmd) && !quiet {
			fmt.Fprintln(os.Stderr, "[abc cloud] Infrastructure mode active — cloud gateway policy applies.")
		} else if utils.SudoFromCmd(cmd) && !quiet {
			fmt.Fprintln(os.Stderr, "[abc sudo] Elevated mode active — policy enforcement delegated to jurist.")
		}
		if utils.ExpFromCmd(cmd) && !quiet {
			fmt.Fprintln(os.Stderr, "[abc exp] Experimental mode active — unstable features may change.")
		}
		return nil
	},
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
	// Elevation flags.
	rootCmd.PersistentFlags().Bool("sudo", false,
		"Elevate to cluster-admin scope (requires admin token; or set ABC_CLI_SUDO_MODE)")
	rootCmd.PersistentFlags().Bool("cloud", false,
		"Elevate to infrastructure scope — fleet-wide + cloud provider APIs (or set ABC_CLI_CLOUD_MODE)")
	rootCmd.PersistentFlags().Bool("exp", false,
		"Enable experimental CLI features (or set ABC_CLI_EXP_MODE)")
	rootCmd.PersistentFlags().String("cluster", utils.EnvOrDefault("ABC_CLUSTER"),
		"Target a specific named cluster in the fleet (requires --cloud; or set ABC_CLUSTER)")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false,
		"Suppress informational output (banners, progress)")

	// Flags for the data command (ABC REST API).
	rootCmd.PersistentFlags().StringVar(&serverURL, "url",
		getEnvOrDefault("ABC_API_ENDPOINT", "https://api.abc-cluster.io"),
		"abc-cluster API endpoint URL (or set ABC_API_ENDPOINT)")
	rootCmd.PersistentFlags().StringVar(&accessToken, "access-token",
		os.Getenv("ABC_ACCESS_TOKEN"),
		"abc-cluster access token (or set ABC_ACCESS_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&workspace, "workspace",
		os.Getenv("ABC_WORKSPACE_ID"),
		"workspace ID (or set ABC_WORKSPACE_ID)")

	rootCmd.AddCommand(pipeline.NewCmd())
	rootCmd.AddCommand(data.NewCmd(&serverURL, &accessToken, &workspace))
	rootCmd.AddCommand(storage.NewCmd())
	rootCmd.AddCommand(job.NewCmd())
	rootCmd.AddCommand(job.NewLogsCmd())
	rootCmd.AddCommand(namespace.NewCmd())
	rootCmd.AddCommand(node.NewCmd())
	rootCmd.AddCommand(cluster.NewCmd())
	rootCmd.AddCommand(budget.NewCmd())
	rootCmd.AddCommand(service.NewCmd())
	rootCmd.AddCommand(service.NewStatusCmd())
}

func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
