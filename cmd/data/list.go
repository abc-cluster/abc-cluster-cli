package data

// list.go — `abc data list` (alias: ls)
//
// Thin wrapper over s5cmd ls. Resolves credentials from the active context,
// prepends --endpoint-url, and execs s5cmd with all remaining args verbatim.
//
// Tool resolution order:
//   ABC_S5CMD_PATH env var → ~/.abc/binaries/s5cmd → system PATH

import (
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var tool string

	cmd := &cobra.Command{
		Use:     "list [s3-uri]",
		Aliases: []string{"ls"},
		Short:   "List objects or buckets",
		Long: `List objects in a bucket or prefix, or list all accessible buckets.

Wraps s5cmd ls (default) or mcli ls (--tool mcli). Credentials and endpoint
are resolved from the active context — no manual configuration needed.

All flags after the S3 URI are forwarded to the underlying tool verbatim.
To see the full flag list of the underlying tool:
  abc data list -- --help

Examples:

  # List all buckets:
  abc data list

  # List objects under a prefix:
  abc data list s3://su-mbhg-hostgen/user/calm-dassie/

  # List with full paths:
  abc data list s3://su-mbhg-hostgen/ --show-fullpath

  # Use mcli instead of s5cmd:
  abc data list s3://su-mbhg-hostgen/ --tool mcli`,
		// DisableFlagParsing lets all flags after the first positional arg pass
		// through to the tool. We parse only --tool ourselves; everything else
		// goes to s5cmd/mcli verbatim.
		DisableFlagParsing: false,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			ctx := cfg.ActiveCtx()
			return runList(ctx, tool, args)
		},
	}

	cmd.Flags().StringVar(&tool, "tool", "s5cmd",
		"underlying tool to use: s5cmd (default) or mcli")

	return cmd
}

func runList(ctx abccfg.Context, tool string, args []string) error {
	switch tool {
	case "mcli":
		return runListMcli(ctx, args)
	default:
		return runListS5cmd(ctx, args)
	}
}

func runListS5cmd(ctx abccfg.Context, args []string) error {
	bin, err := findTool("s5cmd")
	if err != nil {
		return err
	}
	return execTool(bin, s5cmdArgs(ctx, "ls", args), s3Env(ctx))
}

func runListMcli(ctx abccfg.Context, args []string) error {
	bin, alias, tmpDir, cleanup, err := mcliAlias(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	// Rewrite s3://bucket/prefix to alias/bucket/prefix for mcli.
	mcliArgs := append([]string{"ls"}, rewriteS3URIsForMcli(alias, args)...)

	env := append(s3Env(ctx), "MCLI_CONFIG_DIR="+tmpDir)
	return execTool(bin, mcliArgs, env)
}

// rewriteS3URIsForMcli converts s3://bucket/prefix → alias/bucket/prefix
// for any argument that looks like an S3 URI. Non-URI args are passed through.
func rewriteS3URIsForMcli(alias string, args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) > 5 && (a[:5] == "s3://" || a[:5] == "S3://") {
			// s3://bucket/prefix → alias/bucket/prefix
			out[i] = alias + "/" + a[5:]
		} else {
			out[i] = a
		}
	}
	return out
}
