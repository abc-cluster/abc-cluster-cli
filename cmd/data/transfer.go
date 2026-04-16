package data

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
)

// RcloneConfigFetcher loads the merged rclone configuration for the active workspace.
// It is satisfied by abc-khan-svc behind the ABC API base URL. Replace in tests.
var RcloneConfigFetcher = fetchRcloneConfigFromKhan

func fetchRcloneConfigFromKhan(serverURL, accessToken, workspace string) (string, error) {
	return api.NewClient(serverURL, accessToken, workspace).GetKhanRcloneConfig()
}

type transferOptions struct {
	from          string
	to            string
	driver        string
	runName       string
	placementNode string
	parallel      int
	dryRun        bool
	local         bool
	rcloneConfig  string
	toolArgs      string
	move          bool
}

func newCopyCmd(serverURL, accessToken, workspace *string) *cobra.Command {
	opts := &transferOptions{}
	cmd := &cobra.Command{
		Use:   "copy <from> <to>",
		Short: "Copy objects/paths with rclone (Nomad job)",
		Long: `Submit a Nomad batch job that runs rclone copy between two locations.

Without --local, rclone.conf is fetched from the ABC API (abc-khan-svc) for the
active workspace and embedded in the generated task script so credentials reach
the allocation.

With --local, --rclone-config must point to a local rclone.ini; that file is
embedded in the Nomad job script (same mechanism), which shares it with the
cluster as part of the submitted job definition payload.

Examples:
  abc data copy minio-raw:bucket/in s3-out:bucket/out
  abc data copy --local --rclone-config ~/.config/rclone/rclone.conf a: b:
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.from = args[0]
			opts.to = args[1]
			opts.move = false
			return runTransfer(cmd, opts, *serverURL, *accessToken, *workspace)
		},
	}
	addTransferFlags(cmd, opts)
	return cmd
}

func newMoveCmd(serverURL, accessToken, workspace *string) *cobra.Command {
	opts := &transferOptions{}
	cmd := &cobra.Command{
		Use:   "move <from> <to>",
		Short: "Move objects/paths with rclone (Nomad job)",
		Long: `Same as "abc data copy" but runs rclone move (delete source after successful transfer).

Use --dry-run first to preview.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.from = args[0]
			opts.to = args[1]
			opts.move = true
			return runTransfer(cmd, opts, *serverURL, *accessToken, *workspace)
		},
	}
	addTransferFlags(cmd, opts)
	return cmd
}

func addTransferFlags(cmd *cobra.Command, opts *transferOptions) {
	cmd.Flags().StringVar(&opts.driver, "driver", "docker", "nomad task driver: exec, raw_exec, docker, or containerd")
	cmd.Flags().StringVar(&opts.runName, "name", "", "custom Nomad job name")
	cmd.Flags().StringVar(&opts.placementNode, "node", "", "Nomad node placement (UUID or name)")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4, "rclone --transfers parallelism")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "pass --dry-run to rclone")
	cmd.Flags().BoolVar(&opts.local, "local", false, "embed local --rclone-config in the job script instead of fetching from abc-khan-svc")
	cmd.Flags().StringVar(&opts.rcloneConfig, "rclone-config", "", "path to rclone.ini (required with --local)")
	cmd.Flags().StringVar(&opts.toolArgs, "tool-args", "", "extra arguments appended to the rclone invocation")
}

func runTransfer(cmd *cobra.Command, opts *transferOptions, serverURL, accessToken, workspace string) error {
	if strings.TrimSpace(opts.from) == "" || strings.TrimSpace(opts.to) == "" {
		return fmt.Errorf("from and to must be non-empty")
	}
	if opts.local {
		if strings.TrimSpace(opts.rcloneConfig) == "" {
			return fmt.Errorf("--rclone-config is required when using --local")
		}
	} else {
		if strings.TrimSpace(serverURL) == "" {
			return fmt.Errorf("ABC API URL is required (use --url or context) to fetch rclone config from abc-khan-svc")
		}
	}

	d := strings.ToLower(strings.TrimSpace(opts.driver))
	if strings.TrimSpace(opts.placementNode) != "" && (d == "exec" || d == "raw_exec") {
		fmt.Fprintf(cmd.ErrOrStderr(), "[abc] warning: --node pins the job to a node; with --driver=%s the node must have rclone installed. Prefer --driver=docker or --driver=containerd.\n", d)
	}

	var configINI string
	var err error
	if opts.local {
		b, err := os.ReadFile(opts.rcloneConfig)
		if err != nil {
			return fmt.Errorf("read --rclone-config: %w", err)
		}
		configINI = string(b)
	} else {
		configINI, err = RcloneConfigFetcher(serverURL, accessToken, workspace)
		if err != nil {
			return fmt.Errorf("fetch rclone config from API: %w", err)
		}
	}

	parallel := opts.parallel
	if parallel <= 0 {
		parallel = 4
	}

	sub := "copy"
	if opts.move {
		sub = "move"
	}
	script, err := buildRcloneNomadScript(configINI, opts.from, opts.to, sub, parallel, opts.dryRun, opts.toolArgs)
	if err != nil {
		return err
	}

	return submitDataNomadScript(cmd, dataNomadScriptOpts{
		RunName:       opts.runName,
		PlacementNode: opts.placementNode,
		Driver:        opts.driver,
		Tool:          "rclone",
	}, script)
}

func buildRcloneNomadScript(configINI, from, to, subCommand string, transfers int, dryRun bool, toolArgs string) (string, error) {
	if strings.TrimSpace(configINI) == "" {
		return "", fmt.Errorf("rclone config from server is empty; configure remotes in abc-khan-svc for this workspace")
	}
	delimBytes := make([]byte, 8)
	if _, err := rand.Read(delimBytes); err != nil {
		return "", fmt.Errorf("random delimiter: %w", err)
	}
	delim := "ABC_RCLONE_" + hex.EncodeToString(delimBytes) + "_EOF"
	if strings.Contains(configINI, delim) {
		return "", fmt.Errorf("rclone config contains unexpected delimiter sequence; retry")
	}

	var sb strings.Builder
	sb.WriteString("RCLONE_CONF_PATH=\"${RCLONE_CONF_PATH:-/tmp/abc-rclone.conf}\"\n")
	sb.WriteString(fmt.Sprintf("cat > \"$RCLONE_CONF_PATH\" <<'%s'\n", delim))
	sb.WriteString(configINI)
	if !strings.HasSuffix(configINI, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(delim + "\n")
	sb.WriteString("chmod 600 \"$RCLONE_CONF_PATH\" 2>/dev/null || true\n")
	sb.WriteString("export RCLONE_CONFIG=\"$RCLONE_CONF_PATH\"\n")
	sb.WriteString("echo \"=== abc data: rclone " + subCommand + " ===\"\n")

	extra := strings.TrimSpace(toolArgs)
	if extra != "" {
		extra = " " + extra
	}
	dry := ""
	if dryRun {
		dry = " --dry-run"
	}
	line := fmt.Sprintf("rclone %s --config \"$RCLONE_CONF_PATH\" --transfers=%d%s %s %s%s\n",
		subCommand, transfers, dry, shellEscape(from), shellEscape(to), extra)
	sb.WriteString(line)
	return sb.String(), nil
}
