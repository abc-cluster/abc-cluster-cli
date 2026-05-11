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

	"github.com/abc-cluster/abc-cluster-cli/cmd/admin"
	"github.com/abc-cluster/abc-cluster-cli/cmd/auth"
	"github.com/abc-cluster/abc-cluster-cli/cmd/accounting"
	"github.com/abc-cluster/abc-cluster-cli/cmd/cluster"
	"github.com/abc-cluster/abc-cluster-cli/cmd/compliance"
	cfgcmd "github.com/abc-cluster/abc-cluster-cli/cmd/config"
	contextcmd "github.com/abc-cluster/abc-cluster-cli/cmd/auth/context"
	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	localdbcmd "github.com/abc-cluster/abc-cluster-cli/cmd/localdb"
	"github.com/abc-cluster/abc-cluster-cli/cmd/infra"
	"github.com/abc-cluster/abc-cluster-cli/cmd/investigation"
	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/abc-cluster/abc-cluster-cli/cmd/module"
	"github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
	"github.com/abc-cluster/abc-cluster-cli/cmd/project"
	reportcmd "github.com/abc-cluster/abc-cluster-cli/cmd/report"
	"github.com/abc-cluster/abc-cluster-cli/cmd/secrets"
	"github.com/abc-cluster/abc-cluster-cli/cmd/service"
	"github.com/abc-cluster/abc-cluster-cli/cmd/submit"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
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
	Version:       buildVersionString(),
	Long: `abc is the command line interface for the abc-cluster platform.

It allows you to manage and run pipelines and batch jobs on the abc-cluster platform
from your terminal.`,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// ── Debug logging ─────────────────────────────────────────────────────
		// Resolve verbosity level: --debug[=N] flag takes precedence over
		// ABC_CLI_DEBUG env var.
		level, _ := cmd.Root().PersistentFlags().GetInt("debug")
		if level == 0 {
			if v := envvars.Get("ABC_CLI_DEBUG"); v != "" {
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
		maybePrintCLIUpdateNotice(os.Stderr, version, quiet)
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

// commit is set at build time via -ldflags "-X cmd.commit=<git-sha>".
// Falls back to "unknown" when not set.
var commit = "unknown"

func buildVersionString() string {
	short := strings.TrimSpace(commit)
	if short == "" || short == "unknown" {
		return version
	}
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("%s (%s)", version, short)
}

func init() {
	// Couple the build-time CLI version to the state package so the
	// migrations layer can record which CLI version applied each migration.
	state.CLIVersion = buildVersionString()

	cfg, err := config.Load()
	activeCtx := config.Context{}
	if err == nil {
		activeCtx = cfg.ActiveCtx()
	}

	if v := envvars.Get("ABC_API_ADDR"); v != "" {
		serverURL = v
	} else if activeCtx.Endpoint != "" {
		serverURL = activeCtx.Endpoint
	} else {
		serverURL = "https://api.abc-cluster.io"
	}

	if v := envvars.Get("ABC_API_TOKEN"); v != "" {
		accessToken = v
	} else if activeCtx.AccessToken != "" {
		accessToken = activeCtx.AccessToken
	}

	if v := envvars.Get("ABC_WORKSPACE"); v != "" {
		workspace = v
	} else if activeCtx.WorkspaceID != "" {
		workspace = activeCtx.WorkspaceID
	}

	clusterDefault := utils.EnvOrDefault("ABC_CLUSTER")

	// Elevation flags.
	rootCmd.PersistentFlags().Bool("sudo", false,
		"Elevate to cluster-admin scope (requires admin token; or set ABC_CLI_SUDO)")
	rootCmd.PersistentFlags().Bool("cloud", false,
		"Elevate to infrastructure scope — fleet-wide + cloud provider APIs (or set ABC_CLI_CLOUD_MODE)")
	rootCmd.PersistentFlags().Bool("exp", false,
		"Enable experimental CLI features (or set ABC_CLI_EXP_MODE)")
	rootCmd.PersistentFlags().String("cluster", clusterDefault,
		"Target a specific named cluster in the fleet (requires --cloud; or set ABC_CLUSTER)")
	rootCmd.PersistentFlags().String("user", utils.EnvOrDefault("ABC_API_AS_USER"),
		"Act on behalf of this user email — admin only (or set ABC_API_AS_USER)")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false,
		"Suppress informational output (banners, progress)")

	// Debug logging flag.
	// --debug          → level 1 (AI-debuggable events; recommended default)
	// --debug=2        → level 2 (+ remote commands run, raw SSH output)
	// --debug=3        → level 3 (max: SSH round-trips, full config content)
	// ABC_CLI_DEBUG=N      → same as --debug=N via environment variable
	rootCmd.PersistentFlags().Int("debug", 0,
		"Write structured JSON debug log to file (0=off, 1=default, 2=verbose, 3=max).\n"+
			"    Use --debug without a value for level 1. Also ABC_CLI_DEBUG=N.\n"+
			"    Log path is printed to stderr at start and end of run.")
	rootCmd.PersistentFlags().Lookup("debug").NoOptDefVal = "1"

	// Flags for the data command (ABC REST API).
	rootCmd.PersistentFlags().StringVar(&serverURL, "url",
		serverURL,
		"abc-cluster API endpoint URL (or set ABC_API_ADDR)")
	rootCmd.PersistentFlags().StringVar(&accessToken, "access-token",
		accessToken,
		"abc-cluster access token (or set ABC_API_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&workspace, "workspace",
		workspace,
		"workspace ID (or set ABC_WORKSPACE)")

	rootCmd.AddCommand(pipeline.NewCmd())
	rootCmd.AddCommand(module.NewCmd())
	rootCmd.AddCommand(submit.NewSubmitCmd())
	rootCmd.AddCommand(data.NewCmd(&serverURL, &accessToken, &workspace))
	rootCmd.AddCommand(infra.NewCmd())
	rootCmd.AddCommand(admin.NewCmd())
	rootCmd.AddCommand(job.NewCmd())
	rootCmd.AddCommand(job.NewLogsCmd())
	rootCmd.AddCommand(cluster.NewCmd())
	rootCmd.AddCommand(accounting.NewCmd())
	rootCmd.AddCommand(service.NewStatusCmd())
	rootCmd.AddCommand(auth.NewCmd())
	// `abc context` is a deprecated alias of `abc auth context` (kept for one
	// release). The canonical command lives under abc auth context.
	rootCmd.AddCommand(deprecatedRootContextAlias())
	rootCmd.AddCommand(cfgcmd.NewCmd())
	rootCmd.AddCommand(secrets.NewCmd())
	rootCmd.AddCommand(compliance.NewCmd())
	rootCmd.AddCommand(project.NewCmd())
	rootCmd.AddCommand(reportcmd.NewCmd())
	// `abc investigation` is registered as a deprecated root-level alias for
	// one release; the canonical command is `abc project investigation`.
	rootCmd.AddCommand(investigation.NewDeprecatedRootAlias())
	rootCmd.AddCommand(localdbcmd.NewCmd())
	// `abc cache` is a deprecated alias of `abc localdb` (kept for one release).
	rootCmd.AddCommand(localdbcmd.NewDeprecatedCacheAlias())
	// `abc capability` typo redirect — users learning the codename surface
	// from docs sometimes type the singular form. Forward to the canonical
	// `abc cluster capabilities` with a one-line note. Hidden from help.
	rootCmd.AddCommand(capabilityTypoRedirect())
}

// capabilityTypoRedirect makes `abc capability ...` print a one-line
// hint pointing at the canonical command. Hidden from help so it
// doesn't pollute the verb listing.
func capabilityTypoRedirect() *cobra.Command {
	return &cobra.Command{
		Use:                "capability",
		Hidden:             true,
		DisableFlagParsing: true,
		Short:              "(typo redirect) Did you mean 'abc cluster capabilities'?",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr,
				"[abc] no such command 'abc capability'; did you mean 'abc cluster capabilities'?")
			return fmt.Errorf("unknown command 'abc capability' — see 'abc cluster capabilities'")
		},
	}
}

// deprecatedRootContextAlias returns a root-level `abc context` command that
// delegates to `abc auth context` and prints a one-line stderr deprecation
// note on every invocation. Kept for one release window; remove after.
func deprecatedRootContextAlias() *cobra.Command {
	c := contextcmd.NewCmd()
	c.Short = "(deprecated) Manage saved authentication contexts — use 'abc auth context'"
	prev := c.PersistentPreRun
	c.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if !quietMode() {
			fmt.Fprintln(os.Stderr,
				"[abc] note: 'abc context' has been relocated under 'abc auth context'; "+
					"this alias is kept for one release. Update scripts to 'abc auth context ...'.")
		}
		if prev != nil {
			prev(cmd, args)
		}
	}
	return c
}

// quietMode returns true when -q / --quiet is set or ABC_CLI_QUIET=1 in env
// Suppresses the deprecation banner for scripted use.
func quietMode() bool {
	if envvars.IsTruthy("ABC_CLI_QUIET") {
		return true
	}
	if rootCmd != nil {
		if v, _ := rootCmd.PersistentFlags().GetBool("quiet"); v {
			return true
		}
	}
	return false
}

func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
