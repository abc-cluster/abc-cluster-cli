package pulumi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli [pulumi-args...]",
		Short: "Run the local Pulumi CLI with abc context credentials pre-loaded",
		Long: `Run the local pulumi binary as a passthrough alias.

Credentials from the active abc config context are injected as environment
variables so Pulumi providers (Nomad, MinIO) authenticate automatically:

  NOMAD_ADDR          — from admin.services.nomad.nomad_addr
  NOMAD_TOKEN         — from admin.services.nomad.nomad_token
  MINIO_SERVER        — host:port extracted from admin.services.minio endpoint
  MINIO_USER          — from admin.services.minio.cred_source.local.user
  MINIO_PASSWORD      — from admin.services.minio.cred_source.local.password
  PULUMI_ACCESS_TOKEN — from admin.services.pulumi.access_token
  PULUMI_CONFIG_PASSPHRASE — from admin.services.pulumi.config_passphrase

The working directory is changed to admin.services.pulumi.deploy_dir (if set)
before running the command.

Flag handling: this subcommand has DisableFlagParsing=true so abc-level flags
must be placed BEFORE the "cli" subcommand on the parent group, never on this
command itself. Anything after "cli" is forwarded to the pulumi binary verbatim
(except a leading "--binary-location <path>" pair, which is consumed here).

Override Nomad credentials via persistent flags on the parent command:
  abc admin services pulumi --nomad-addr ... --nomad-token ... cli -- up --yes

Use -- to pass argv verbatim to pulumi (recommended):
  abc admin services pulumi cli -- up --yes
  abc admin services pulumi cli -- destroy --yes
  abc admin services pulumi cli -- stack output --json

Optional leading --binary-location <path> before --:
  abc admin services pulumi cli --binary-location /usr/local/bin/pulumi -- up --yes

Set ABC_DEBUG=1 to log which MinIO credential source is being injected.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runPulumiCLI,
	}
	return cmd
}

func runPulumiCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	binaryLocation = resolvePulumiBinary(binaryLocation)

	// Resolve credentials from the abc config context.
	envOverrides := buildPulumiEnv(cmd)

	// Change working directory to deploy_dir if configured.
	if deployDir := pulumiDeployDirFromConfig(); deployDir != "" {
		abs, err := filepath.Abs(deployDir)
		if err == nil {
			deployDir = abs
		}
		if err := os.Chdir(deployDir); err != nil {
			return err
		}
	}

	return utils.RunExternalCLIWithEnv(
		cmd.Context(),
		passthroughArgs,
		binaryLocation,
		[]string{"pulumi"},
		envOverrides,
		os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(),
	)
}

// resolvePulumiBinary returns binaryLocation if set, otherwise searches
// ABC_PULUMI_CLI_BINARY / PULUMI_CLI_BINARY / PULUMI_BINARY env vars,
// then falls back to the managed binary at ~/.abc/binaries/pulumi.
func resolvePulumiBinary(binaryLocation string) string {
	if binaryLocation != "" {
		return binaryLocation
	}
	if loc := utils.EnvOrDefault(
		"ABC_PULUMI_CLI_BINARY",
		"PULUMI_CLI_BINARY",
		"PULUMI_BINARY",
	); loc != "" {
		return loc
	}
	if managedPath, err := utils.ManagedBinaryPath("pulumi"); err == nil {
		if info, err := os.Stat(managedPath); err == nil && !info.IsDir() {
			return managedPath
		}
	}
	return "" // RunExternalCLI will search PATH
}

// pulumiDeployDirFromConfig returns the deploy_dir from the active context's
// pulumi service config, or "" if unset.
func pulumiDeployDirFromConfig() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.ActiveCtx().PulumiDeployDir()
}

// pulumiConnectionFromCmd resolves Nomad credentials for env injection.
// Priority: persistent flags set on the parent cmd → config file → empty.
func pulumiConnectionFromCmd(cmd *cobra.Command) (addr, token string) {
	addr, token = pulumiNomadDefaultsFromConfig()

	parentCmd := cmd.Parent()
	if parentCmd != nil {
		if f := parentCmd.PersistentFlags().Lookup("nomad-addr"); f != nil && f.Changed {
			addr = f.Value.String()
		} else if addr == "" {
			addr = utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR")
		}

		if f := parentCmd.PersistentFlags().Lookup("nomad-token"); f != nil && f.Changed {
			token = f.Value.String()
		} else if token == "" {
			token = utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN")
		}
	}
	return addr, token
}

func pulumiNomadDefaultsFromConfig() (addr, token string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", ""
	}
	active := cfg.ActiveCtx()
	return active.NomadAddr(), active.NomadToken()
}

// minioCredsFromConfig extracts MinIO credentials from the active context's
// minio service config for injection into Pulumi provider env vars.
//
// Source precedence:
//  1. admin.services.minio.cred_source.local.{endpoint|http,user,password} —
//     preferred; matches the schema used by `abc admin services minio` for
//     local-cred sourcing.
//  2. admin.services.minio.{endpoint|http,user,password} — top-level fields,
//     kept as a fallback for older config files that predate cred_source.
//
// Returned strings are passed to the @pulumi/minio provider as MINIO_SERVER
// (host:port, scheme stripped), MINIO_USER, and MINIO_PASSWORD.
func minioCredsFromConfig() (server, user, password string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", "", ""
	}
	active := cfg.ActiveCtx()
	svc := active.Admin.Services.MinIO
	if svc == nil {
		return "", "", ""
	}

	source := "cred_source.local"
	if svc.CredSource != nil && len(svc.CredSource.Local) > 0 {
		local := svc.CredSource.Local
		endpoint := local["endpoint"]
		if endpoint == "" {
			endpoint = local["http"]
		}
		server = stripScheme(endpoint)
		user = local["user"]
		password = local["password"]
	} else {
		source = "top-level"
		server = stripScheme(svc.Endpoint)
		if server == "" {
			server = stripScheme(svc.HTTP)
		}
		user = svc.User
		password = svc.Password
	}

	if os.Getenv("ABC_DEBUG") != "" && server != "" {
		fmt.Fprintf(os.Stderr, "[abc pulumi] injecting MinIO creds from %s (server=%s, user=%s)\n", source, server, user)
	}
	return server, user, password
}

// pulumiTokensFromConfig returns Pulumi Cloud access token and config passphrase.
func pulumiTokensFromConfig() (accessToken, configPassphrase string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", ""
	}
	active := cfg.ActiveCtx()
	return active.PulumiAccessToken(), active.PulumiConfigPassphrase()
}

// buildPulumiEnv assembles the env var map to inject into the pulumi process.
// Only non-empty values are included; existing process env vars with the same
// name are overridden (highest-priority: flag overrides from the parent cmd).
func buildPulumiEnv(cmd *cobra.Command) map[string]string {
	env := map[string]string{}

	// Nomad provider credentials.
	addr, token := pulumiConnectionFromCmd(cmd)
	if addr != "" {
		env["NOMAD_ADDR"] = addr
	}
	if token != "" {
		env["NOMAD_TOKEN"] = token
	}

	// MinIO provider credentials.
	minioServer, minioUser, minioPassword := minioCredsFromConfig()
	if minioServer != "" {
		env["MINIO_SERVER"] = minioServer
	}
	if minioUser != "" {
		env["MINIO_USER"] = minioUser
	}
	if minioPassword != "" {
		env["MINIO_PASSWORD"] = minioPassword
	}

	// Pulumi-specific tokens.
	accessToken, configPassphrase := pulumiTokensFromConfig()
	if accessToken != "" {
		env["PULUMI_ACCESS_TOKEN"] = accessToken
	}
	if configPassphrase != "" {
		env["PULUMI_CONFIG_PASSPHRASE"] = configPassphrase
	}

	return env
}

// stripScheme removes http:// or https:// from a URL, returning just host:port.
func stripScheme(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "http://") {
		return u[len("http://"):]
	}
	if strings.HasPrefix(u, "https://") {
		return u[len("https://"):]
	}
	return u
}
