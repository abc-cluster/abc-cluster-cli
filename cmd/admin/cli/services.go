package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// RegisterServices wires every service CLI subcommand onto the given parent
// command. It is called both from cli.NewCmd (standalone use) and from the
// existing "abc admin services cli" command in cmd/service so the per-service
// passthrough subcommands are available at abc admin services cli <tool>.
func RegisterServices(parent *cobra.Command) {
	// --- services with credential injection ---
	parent.AddCommand(newPulumiCmd())
	parent.AddCommand(newTerraformCmd())
	parent.AddCommand(newNomadCmd())
	parent.AddCommand(newNomadPackCmd())
	parent.AddCommand(newMinioCmd())
	parent.AddCommand(newVaultCmd())
	parent.AddCommand(newLokiCmd())
	parent.AddCommand(newRustfsCmd())

	// --- pure passthrough (no credential injection) ---
	parent.AddCommand(newPassthrough("rclone", "rclone", "Run the local rclone binary", []string{"rclone"}))
	parent.AddCommand(newPassthrough("boundary", "boundary", "Run the local Boundary CLI", []string{"boundary"}))
	parent.AddCommand(newPassthrough("consul", "consul", "Run the local Consul CLI", []string{"consul"}))
	parent.AddCommand(newPassthrough("eget", "eget", "Run the local eget binary downloader", []string{"eget"}))
	parent.AddCommand(newPassthrough("nebula", "nebula", "Run the local Nebula overlay VPN CLI", []string{"nebula"}))
	parent.AddCommand(newPassthrough("ntfy", "ntfy", "Run the local ntfy CLI", []string{"ntfy"}))
	parent.AddCommand(newPassthrough("grafana", "grafana", "Run the local Grafana CLI (grafana-cli or grafana cli)", []string{"grafana-cli", "grafana"}))
	parent.AddCommand(newPassthrough("tailscale", "tailscale", "Run the local Tailscale CLI", []string{"tailscale"}))
	parent.AddCommand(newPassthrough("traefik", "traefik", "Run the local Traefik CLI", []string{"traefik"}))
	parent.AddCommand(newPassthrough("hashi-up", "hashi-up", "Run the local hashi-up CLI", []string{"hashi-up", "hashiup"}))
	parent.AddCommand(newPassthrough("postgres", "psql", "Run the local psql (PostgreSQL CLI)", []string{"psql"}))
}

// ─────────────────────────────────────────────────────────────────────────────
// Generic passthrough factory (no credential injection)
// ─────────────────────────────────────────────────────────────────────────────

// newPassthrough returns a DisableFlagParsing subcommand that delegates to the
// named binary without injecting any additional environment variables.
func newPassthrough(name, managedBinaryName, short string, binaries []string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [args...]",
		Short:              short,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
			if err != nil {
				return err
			}
			if binaryLocation == "" {
				if managed, mErr := utils.ManagedBinaryPath(managedBinaryName); mErr == nil {
					if info, sErr := os.Stat(managed); sErr == nil && !info.IsDir() {
						binaryLocation = managed
					}
				}
			}
			return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, binaries, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Nomad credentials helper (shared by pulumi, terraform, nomad, nomad-pack)
// ─────────────────────────────────────────────────────────────────────────────

// nomadCredsFromParent resolves the Nomad address and token.
// Priority: persistent flags on the parent cli command → config → env vars.
func nomadCredsFromParent(cmd *cobra.Command) (addr, token string) {
	// Load defaults from active context.
	if cfg, err := config.Load(); err == nil && cfg != nil {
		active := cfg.ActiveCtx()
		addr, token = active.NomadAddr(), active.NomadToken()
	}

	// Flag overrides (flags live on cmd.Parent() = the "cli" command).
	if parent := cmd.Parent(); parent != nil {
		if f := parent.PersistentFlags().Lookup("nomad-addr"); f != nil && f.Changed {
			addr = f.Value.String()
		}
		if f := parent.PersistentFlags().Lookup("nomad-token"); f != nil && f.Changed {
			token = f.Value.String()
		}
	}

	// Env var fallback.
	if addr == "" {
		addr = utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR")
	}
	if token == "" {
		token = utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN")
	}
	return addr, token
}

// ─────────────────────────────────────────────────────────────────────────────
// Pulumi
// ─────────────────────────────────────────────────────────────────────────────

func newPulumiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pulumi [args...]",
		Short: "Run the local Pulumi CLI with Nomad, MinIO, and Pulumi credentials pre-loaded",
		Long: `Passthrough to the pulumi binary.

Injects from active context:
  NOMAD_ADDR, NOMAD_TOKEN          — admin.services.nomad
  MINIO_SERVER, MINIO_USER,
  MINIO_PASSWORD                   — admin.services.minio
  PULUMI_ACCESS_TOKEN              — admin.services.pulumi.access_token
  PULUMI_CONFIG_PASSPHRASE         — admin.services.pulumi.config_passphrase

Changes working directory to admin.services.pulumi.deploy_dir before running.

  abc admin services cli pulumi -- up --yes
  abc admin services cli pulumi -- destroy --yes
  abc admin services cli pulumi -- stack output --json`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runPulumiCLI,
	}
}

func runPulumiCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_PULUMI_CLI_BINARY", "PULUMI_CLI_BINARY", "PULUMI_BINARY")
		if binaryLocation == "" {
			if managed, mErr := utils.ManagedBinaryPath("pulumi"); mErr == nil {
				if info, sErr := os.Stat(managed); sErr == nil && !info.IsDir() {
					binaryLocation = managed
				}
			}
		}
	}

	env := map[string]string{}

	nomadAddr, nomadToken := nomadCredsFromParent(cmd)
	if nomadAddr != "" {
		env["NOMAD_ADDR"] = nomadAddr
	}
	if nomadToken != "" {
		env["NOMAD_TOKEN"] = nomadToken
	}

	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		active := cfg.ActiveCtx()

		// MinIO credentials.
		minioSvc := active.Admin.Services.MinIO
		if minioSvc != nil {
			var endpoint, user, password string
			if minioSvc.CredSource != nil && len(minioSvc.CredSource.Local) > 0 {
				local := minioSvc.CredSource.Local
				endpoint = local["endpoint"]
				if endpoint == "" {
					endpoint = local["http"]
				}
				user = local["user"]
				password = local["password"]
			} else {
				endpoint = minioSvc.Endpoint
				if endpoint == "" {
					endpoint = minioSvc.HTTP
				}
				user = minioSvc.User
				password = minioSvc.Password
			}
			if s := stripScheme(endpoint); s != "" {
				env["MINIO_SERVER"] = s
			}
			if user != "" {
				env["MINIO_USER"] = user
			}
			if password != "" {
				env["MINIO_PASSWORD"] = password
			}
		}

		// Pulumi-specific tokens.
		if tok := active.PulumiAccessToken(); tok != "" {
			env["PULUMI_ACCESS_TOKEN"] = tok
		}
		if pp := active.PulumiConfigPassphrase(); pp != "" {
			env["PULUMI_CONFIG_PASSPHRASE"] = pp
		}

		// Change to deploy_dir.
		if deployDir := active.PulumiDeployDir(); deployDir != "" {
			abs, absErr := filepath.Abs(deployDir)
			if absErr == nil {
				deployDir = abs
			}
			if cdErr := os.Chdir(deployDir); cdErr != nil {
				return cdErr
			}
		}
	}

	return utils.RunExternalCLIWithEnv(cmd.Context(), passthroughArgs, binaryLocation, []string{"pulumi"}, env, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// Terraform
// ─────────────────────────────────────────────────────────────────────────────

func newTerraformCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "terraform [args...]",
		Short: "Run the local Terraform CLI with Nomad credentials and TF_VAR_* pre-loaded",
		Long: `Passthrough to the terraform binary.

Injects from active context:
  NOMAD_ADDR, NOMAD_TOKEN, NOMAD_REGION   — admin.services.nomad
  TF_VAR_nomad_address, TF_VAR_nomad_token,
  TF_VAR_nomad_region                     — same values as TF variables
  TF_VAR_<key>                            — admin.services.terraform.vars

Changes working directory to admin.services.terraform.deploy_dir before running.

  abc admin services cli terraform -- init
  abc admin services cli terraform -- plan
  abc admin services cli terraform -- apply -auto-approve`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTerraformCLI,
	}
}

func runTerraformCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_TERRAFORM_CLI_BINARY", "TERRAFORM_CLI_BINARY", "TERRAFORM_BINARY")
		if binaryLocation == "" {
			if managed, mErr := utils.ManagedBinaryPath("terraform"); mErr == nil {
				if info, sErr := os.Stat(managed); sErr == nil && !info.IsDir() {
					binaryLocation = managed
				}
			}
		}
	}

	addr, token := nomadCredsFromParent(cmd)
	var region string

	env := map[string]string{}

	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		active := cfg.ActiveCtx()
		if addr == "" {
			addr = active.NomadAddr()
		}
		if token == "" {
			token = active.NomadToken()
		}
		region = active.NomadRegion()

		// Extra TF_VAR_* from admin.services.terraform.vars (lowest priority).
		for k, v := range active.TerraformVars() {
			if k != "" && v != "" {
				env["TF_VAR_"+k] = v
			}
		}

		// Change to deploy_dir so terraform finds its .tf files.
		if deployDir := active.TerraformDeployDir(); deployDir != "" {
			abs, absErr := filepath.Abs(deployDir)
			if absErr == nil {
				deployDir = abs
			}
			if cdErr := os.Chdir(deployDir); cdErr != nil {
				return cdErr
			}
		}
	}

	if addr != "" {
		env["NOMAD_ADDR"] = addr
		env["TF_VAR_nomad_address"] = addr
	}
	if token != "" {
		env["NOMAD_TOKEN"] = token
		env["TF_VAR_nomad_token"] = token
	}
	if region != "" {
		env["NOMAD_REGION"] = region
		env["TF_VAR_nomad_region"] = region
	}

	return utils.RunExternalCLIWithEnv(cmd.Context(), passthroughArgs, binaryLocation, []string{"terraform"}, env, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// Nomad
// ─────────────────────────────────────────────────────────────────────────────

func newNomadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nomad [args...]",
		Short: "Run the local Nomad CLI with context credentials pre-loaded",
		Long: `Passthrough to the nomad binary.

Injects from active context:
  NOMAD_ADDR, NOMAD_TOKEN, NOMAD_REGION, NOMAD_NAMESPACE

  abc admin services cli nomad -- job status -short
  abc admin services cli nomad -- acl token self`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNomadCLI,
	}
}

func runNomadCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NOMAD_CLI_BINARY", "NOMAD_CLI_BINARY", "NOMAD_BINARY")
		if binaryLocation == "" {
			if managed, mErr := utils.ManagedBinaryPath("nomad"); mErr == nil {
				if info, sErr := os.Stat(managed); sErr == nil && !info.IsDir() {
					binaryLocation = managed
				}
			}
		}
	}
	addr, token := nomadCredsFromParent(cmd)
	var region string
	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		region = cfg.ActiveCtx().NomadRegion()
	}
	return utils.RunNomadCLI(cmd.Context(), passthroughArgs, binaryLocation, addr, token, region, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// Nomad Pack
// ─────────────────────────────────────────────────────────────────────────────

func newNomadPackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nomad-pack [args...]",
		Short: "Run the local nomad-pack CLI with context credentials pre-loaded",
		Long: `Passthrough to the nomad-pack binary.

Injects: NOMAD_ADDR, NOMAD_TOKEN, NOMAD_REGION, NOMAD_NAMESPACE

  abc admin services cli nomad-pack -- plan ./packs/hello-world`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNomadPackCLI,
	}
}

func runNomadPackCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NOMAD_PACK_CLI_BINARY", "NOMAD_PACK_CLI_BINARY", "NOMAD_PACK_BINARY")
		if binaryLocation == "" {
			if managed, mErr := utils.ManagedBinaryPath("nomad-pack"); mErr == nil {
				if info, sErr := os.Stat(managed); sErr == nil && !info.IsDir() {
					binaryLocation = managed
				}
			}
		}
	}
	return utils.RunNomadPackCLI(cmd.Context(), passthroughArgs, binaryLocation, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// MinIO
// ─────────────────────────────────────────────────────────────────────────────

func newMinioCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "minio [args...]",
		Short: "Run the local MinIO client CLI (mcli/mc) with S3 credentials pre-loaded",
		Long: `Passthrough to mcli or mc.

Injects from active context:
  AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_ENDPOINT_URL,
  MINIO_ROOT_USER, MINIO_ROOT_PASSWORD

  abc admin services cli minio -- ls local
  abc admin services cli minio -- mb local/my-bucket`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runMinioCLI,
	}
}

func runMinioCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_MINIO_CLI_BINARY", "MINIO_CLI_BINARY", "MCLI_BINARY", "MC_BINARY")
	}

	base := os.Environ()
	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		env, rerr := utils.ResolvedAbcNodesStorageCLIEnv(cmd.Context(), cfg, "minio", "local")
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"mcli", "mc"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// Vault
// ─────────────────────────────────────────────────────────────────────────────

func newVaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vault [args...]",
		Short: "Run the local Vault / OpenBao CLI with VAULT_ADDR and VAULT_TOKEN pre-loaded",
		Long: `Passthrough to vault or bao.

Injects from active context:
  VAULT_ADDR, VAULT_TOKEN   — admin.services.vault

  abc admin services cli vault status
  abc admin services cli vault kv get secret/mykey`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runVaultCLI,
	}
}

func runVaultCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_VAULT_CLI_BINARY", "VAULT_CLI_BINARY", "VAULT_BINARY",
			"ABC_BAO_CLI_BINARY", "BAO_CLI_BINARY", "BAO_BINARY",
			"ABC_OPENBAO_CLI_BINARY", "OPENBAO_CLI_BINARY", "OPENBAO_BINARY",
		)
	}

	base := os.Environ()
	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		env, rerr := utils.ResolvedVaultCLIEnv(cmd.Context(), cfg, "local")
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"vault", "bao", "openbao"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// Loki (logcli)
// ─────────────────────────────────────────────────────────────────────────────

func newLokiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "loki [args...]",
		Short: "Run the local logcli binary (Loki CLI) with LOKI_ADDR pre-set",
		Long: `Passthrough to logcli.

Injects: LOKI_ADDR — from admin.services.loki

  abc admin services cli loki -- query '{job="nomad"}' --limit 100`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runLokiCLI,
	}
}

func runLokiCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_LOGCLI_BINARY", "LOGCLI_BINARY")
	}

	base := os.Environ()
	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		active := cfg.ActiveCtx()
		svc := config.AdminFloorServiceNamed(&active.Admin.Services, "loki")
		lokiHTTP, rerr := utils.ResolveAdminFloorField(cmd.Context(), active, svc, "loki", "local", "http")
		if rerr != nil {
			return rerr
		}
		if lokiHTTP != "" {
			base = utils.UpsertEnvOnlyMissing(base, map[string]string{"LOKI_ADDR": lokiHTTP})
		}
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"logcli"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// RustFS
// ─────────────────────────────────────────────────────────────────────────────

func newRustfsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rustfs [args...]",
		Short: "Run the local RustFS CLI with S3 credentials pre-loaded",
		Long: `Passthrough to the rustfs binary.

Injects: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_ENDPOINT_URL — admin.services.rustfs

  abc admin services cli rustfs -- ls`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runRustfsCLI,
	}
}

func runRustfsCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_RUSTFS_CLI_BINARY", "RUSTFS_CLI_BINARY", "RUSTFS_BINARY")
	}

	base := os.Environ()
	if cfg, cerr := config.Load(); cerr == nil && cfg != nil {
		env, rerr := utils.ResolvedAbcNodesStorageCLIEnv(cmd.Context(), cfg, "rustfs", "local")
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"rustfs"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// stripScheme removes http:// or https:// from a URL, returning just host:port.
func stripScheme(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case strings.HasPrefix(u, "https://"):
		return u[len("https://"):]
	case strings.HasPrefix(u, "http://"):
		return u[len("http://"):]
	}
	return u
}
