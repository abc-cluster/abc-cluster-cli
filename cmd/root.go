package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/spf13/cobra"
)

// serverURL / accessToken / workspace are used by the data command which
// still targets the ABC REST API.
var (
	serverURL   string
	accessToken string
	workspace   string
)

// activeDebugCfg holds the current run's debug config so Execute() can close
// the log file and print the footer after the command completes (or errors).
// Safe as a package-level var because cobra commands run sequentially.
var activeDebugCfg *debuglog.Config

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
		// ── Debug logging ─────────────────────────────────────────────────────
		// Resolve verbosity level: --debug[=N] flag takes precedence over
		// ABC_DEBUG env var.
		level, _ := cmd.Root().PersistentFlags().GetInt("debug")
		if level == 0 {
			if v := os.Getenv("ABC_DEBUG"); v != "" {
				level, _ = strconv.Atoi(v)
			}
		}
		ctx, cfg, err := debuglog.Init(cmd.Context(), level)
		if err != nil {
			// Non-fatal: warn but continue without logging.
			fmt.Fprintf(os.Stderr, "[abc] warning: debug log init failed: %v\n", err)
		}
		activeDebugCfg = cfg
		if cfg.Enabled {
			cfg.PrintHeader(os.Stderr)
		}
		cmd.SetContext(ctx)

		// First structured event: full CLI invocation with argv (secrets redacted).
		log := debuglog.FromContext(ctx)
		log.LogAttrs(ctx, debuglog.L1, "cli.invocation",
			debuglog.AttrsCLIInvocation(
				debuglog.RedactArgv(os.Args),
				debuglog.EnvSnapshot(),
				version,
			)...,
		)

		// ── Mode banners ──────────────────────────────────────────────────────
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

	runErr := rootCmd.ExecuteContext(ctx)

	// Print the debug log footer (path + failure hint) after the command
	// completes — whether it succeeded or failed. This runs even when
	// PersistentPostRunE is skipped due to an error.
	if activeDebugCfg != nil {
		activeDebugCfg.PrintFooter(os.Stderr, runErr)
		activeDebugCfg.Close()
	}

	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			action := cancelledActionFromArgs(os.Args[1:])
			if action == "" {
				fmt.Fprintln(os.Stderr, "cancelled")
			} else {
				fmt.Fprintf(os.Stderr, "cancelled: %s\n", action)
			}
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, runErr)
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

// version is set at build time via -ldflags "-X cmd.version=v1.2.3".
// Falls back to "dev" when not set.
var version = "dev"

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

	// Debug logging flag.
	// --debug          → level 1 (AI-debuggable events; recommended default)
	// --debug=2        → level 2 (+ remote commands run, raw SSH output)
	// --debug=3        → level 3 (max: SSH round-trips, full config content)
	// ABC_DEBUG=N      → same as --debug=N via environment variable
	rootCmd.PersistentFlags().Int("debug", 0,
		"Write structured JSON debug log to file (0=off, 1=default, 2=verbose, 3=max).\n"+
			"    Use --debug without a value for level 1. Also ABC_DEBUG=N.\n"+
			"    Log path is printed to stderr at start and end of run.")
	rootCmd.PersistentFlags().Lookup("debug").NoOptDefVal = "1"

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
