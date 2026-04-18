package rclone

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli [rclone-args...]",
		Short: "Run the local rclone CLI with optional workspace config",
		Long: `Run the rclone binary as a passthrough alias. Use "abc admin services rclone cli setup"
to install the managed binary into ~/.abc/binaries (or ABC_BINARIES_DIR). Use
--binary-location to select a specific rclone binary (same convention as nomad/tailscale).

Optional leading arguments (stripped before invoking rclone):
  --abc-server-config          Fetch rclone.ini from GET {--url}/khan/v1/rclone.conf and set RCLONE_CONFIG to a temp file
  --abc-local-config=<path>    Set RCLONE_CONFIG to this path (no network fetch)

Use "--" so arbitrary rclone flags (including "-h" / "--config") are forwarded verbatim:
  abc admin services rclone cli --abc-server-config -- lsd remote:
  abc admin services rclone cli --binary-location /path/rclone -- --version

Optional wrappers (--binary-location, --abc-server-config, --abc-local-config) are only
recognized at the start of argv. After them, either a lone "--" introduces verbatim
rclone args, or the remaining argv is passed to rclone after stripping any leading
--abc-* flags.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runRcloneCLI,
	}
	return cmd
}

func runRcloneCLI(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "setup" {
		return runRcloneCLISetup(cmd)
	}

	binaryLocation, serverConfig, localConfig, passthrough, err := extractRcloneCLIPassthrough(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_RCLONE_CLI_BINARY", "RCLONE_CLI_BINARY", "RCLONE_BINARY")
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("rclone"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	serverURL, accessToken, workspace := apiConnectionFromRoot(cmd)

	env := map[string]string{}
	var cleanup func()
	if serverConfig {
		if strings.TrimSpace(serverURL) == "" {
			return fmt.Errorf("--abc-server-config requires ABC API --url (or active context endpoint)")
		}
		ini, err := api.NewClient(serverURL, accessToken, workspace).GetKhanRcloneConfig()
		if err != nil {
			return fmt.Errorf("fetch rclone config: %w", err)
		}
		f, err := os.CreateTemp("", "abc-rclone-*.conf")
		if err != nil {
			return err
		}
		path := f.Name()
		if _, err := f.WriteString(ini); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(path)
			return err
		}
		_ = os.Chmod(path, 0600)
		env["RCLONE_CONFIG"] = path
		cleanup = func() { _ = os.Remove(path) }
	} else if localConfig != "" {
		abs, err := filepath.Abs(localConfig)
		if err != nil {
			return fmt.Errorf("resolve --abc-local-config: %w", err)
		}
		env["RCLONE_CONFIG"] = abs
	}
	if cleanup != nil {
		defer cleanup()
	}

	return utils.RunExternalCLIWithEnv(cmd.Context(), passthrough, binaryLocation, []string{"rclone"}, env, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func runRcloneCLISetup(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	dir, err := utils.ManagedBinaryDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Setting up wrapped binaries in %s\n", dir)
	fmt.Fprintln(out, "Checking PATH first, then downloading missing binaries...")
	if _, err := utils.SetupRcloneBinary(out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Setup complete.")
	fmt.Fprintf(out, "Tip: prepend %s to PATH to prefer managed binaries.\n", dir)
	return nil
}

func apiConnectionFromRoot(cmd *cobra.Command) (serverURL, accessToken, workspace string) {
	root := cmd.Root()
	serverURL, _ = root.PersistentFlags().GetString("url")
	accessToken, _ = root.PersistentFlags().GetString("access-token")
	workspace, _ = root.PersistentFlags().GetString("workspace")
	return serverURL, accessToken, workspace
}

// extractRcloneCLIPassthrough consumes optional leading --binary-location and --abc-*
// wrappers, then either a "--" verbatim split or parseABCRclonePreamble on the tail.
func extractRcloneCLIPassthrough(args []string) (binaryLocation string, serverConfig bool, localConfig string, passthrough []string, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--binary-location":
			if i+1 >= len(args) {
				return "", false, "", nil, fmt.Errorf("--binary-location requires a value")
			}
			binaryLocation = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--binary-location="):
			binaryLocation = strings.TrimPrefix(a, "--binary-location=")
			i++
		case a == "--abc-server-config":
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-server-config")
			}
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			serverConfig = true
			i++
		case strings.HasPrefix(a, "--abc-local-config="):
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-local-config")
			}
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			localConfig = strings.TrimPrefix(a, "--abc-local-config=")
			i++
		case a == "--abc-local-config":
			if i+1 >= len(args) {
				return "", false, "", nil, fmt.Errorf("--abc-local-config requires a path")
			}
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-local-config")
			}
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			localConfig = args[i+1]
			i += 2
		case a == "--":
			return binaryLocation, serverConfig, localConfig, append([]string(nil), args[i+1:]...), nil
		default:
			return parseABCRclonePreamble(binaryLocation, args[i:])
		}
	}
	return parseABCRclonePreamble(binaryLocation, nil)
}

// parseABCRclonePreamble strips abc-only flags. binaryLocation is passed through unchanged.
func parseABCRclonePreamble(binaryLocation string, args []string) (binOut string, serverConfig bool, localConfig string, rest []string, err error) {
	binOut = binaryLocation
	rest = args
	for len(rest) > 0 {
		a := rest[0]
		switch {
		case a == "--abc-server-config":
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-server-config")
			}
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			serverConfig = true
			rest = rest[1:]
		case strings.HasPrefix(a, "--abc-local-config="):
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-local-config")
			}
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			localConfig = strings.TrimPrefix(a, "--abc-local-config=")
			rest = rest[1:]
		case a == "--abc-local-config":
			if len(rest) < 2 {
				return "", false, "", nil, fmt.Errorf("--abc-local-config requires a path")
			}
			if localConfig != "" {
				return "", false, "", nil, fmt.Errorf("duplicate --abc-local-config")
			}
			if serverConfig {
				return "", false, "", nil, fmt.Errorf("cannot combine --abc-server-config with --abc-local-config")
			}
			localConfig = rest[1]
			rest = rest[2:]
		default:
			return binOut, serverConfig, localConfig, rest, nil
		}
	}
	return binOut, serverConfig, localConfig, rest, nil
}
