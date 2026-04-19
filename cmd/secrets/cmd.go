// Package secrets implements the "abc secrets" command group.
//
// Supports three backends:
//   - local   — encrypted secrets stored in ~/.abc/config.yaml (password-based)
//   - nomad   — secrets stored in Nomad Variables at abc/secrets/<ns>/<name>
//   - vault   — secrets stored in Vault KV v2 at secret/data/abc/<ns>/<name>
//
// Admin role: set/delete require a token with write access to the backend.
// User role:  list shows key names; get is intentionally blocked for cluster-side
//             backends (secrets reach jobs via template nomadVar / vault stanza, not CLI).
//
// Default backend: "local" for backward compatibility.
// Override: --backend nomad | vault, or set capabilities.secrets in config after sync.
package secrets

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the "secrets" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage secrets (local, Nomad Variables, or Vault KV v2)",
		Long: `Manage secrets across three backends:

  local  — encrypted in ~/.abc/config.yaml (password-based)
  nomad  — Nomad Variables at abc/secrets/<namespace>/<name>
  vault  — Vault KV v2 at secret/data/abc/<namespace>/<name>

Admin commands (set/delete) require a token with write access.
User read access: secrets reach jobs via HCL template references —
use 'abc secrets ref <name>' to get the template snippet.

Examples:
  # Local encrypted storage (backward-compatible)
  abc secrets init --unsafe-local
  abc secrets set my-key "value" --unsafe-local
  abc secrets get my-key --unsafe-local

  # Nomad Variables (admin)
  abc secrets set db-password "s3cr3t" --backend nomad
  abc secrets list --backend nomad
  abc secrets ref db-password --backend nomad

  # Vault KV v2 (admin, requires VAULT_TOKEN)
  abc secrets set db-password "s3cr3t" --backend vault
  abc secrets ref db-password --backend vault`,
	}

	// Backend selection and cluster-side connection flags (used by nomad + vault backends).
	cmd.PersistentFlags().String("backend", "local", "Secret backend: local | nomad | vault")
	cmd.PersistentFlags().String("namespace", "", "Nomad namespace (default: active context abc_nodes.nomad_namespace or 'default')")
	cmd.PersistentFlags().String("nomad-addr", "", "Nomad API address (overrides config)")
	cmd.PersistentFlags().String("nomad-token", "", "Nomad ACL token (overrides config)")
	cmd.PersistentFlags().String("region", "", "Nomad region")
	cmd.PersistentFlags().String("vault-addr", "", "Vault address (overrides VAULT_ADDR env and config)")
	cmd.PersistentFlags().String("vault-token", "", "Vault token (overrides VAULT_TOKEN env)")

	cmd.AddCommand(
		newInitCmd(),
		newSetCmd(),
		newGetCmd(),
		newListCmd(),
		newDeleteCmd(),
		newRefCmd(),
		newBackendCmd(),
	)

	return cmd
}

func backendFromCmd(cmd *cobra.Command) string {
	b, _ := cmd.Flags().GetString("backend")
	if b == "" {
		b, _ = cmd.Root().PersistentFlags().GetString("backend")
	}
	return b
}

// ── set ───────────────────────────────────────────────────────────────────────

func newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Store a secret",
		Long: `Store a secret in the selected backend.

  local  — requires --unsafe-local and ABC_CRYPT_PASSWORD
  nomad  — requires admin Nomad token with write access to abc/secrets/*
  vault  — requires VAULT_TOKEN (or --vault-token) with write access`,
		Args: cobra.ExactArgs(2),
		RunE: runSetSecret,
	}
	cmd.Flags().Bool("unsafe-local", false, "Allow writing encrypted secrets to local config (local backend)")
	return cmd
}

func runSetSecret(cmd *cobra.Command, args []string) error {
	backend := backendFromCmd(cmd)
	name, value := args[0], args[1]

	switch backend {
	case "nomad":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runNomadSet(cmd, cfg, name, value)
	case "vault":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runVaultSet(cmd, cfg, name, value)
	default:
		return runLocalSet(cmd, name, value)
	}
}

// ── get ───────────────────────────────────────────────────────────────────────

func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Retrieve a secret",
		Long: `Retrieve and output a secret from the selected backend.

  local  — requires --unsafe-local and ABC_CRYPT_PASSWORD
  nomad  — admin token required; non-admins should use 'abc secrets ref' instead
  vault  — requires VAULT_TOKEN with read access`,
		Args: cobra.ExactArgs(1),
		RunE: runGetSecret,
	}
	cmd.Flags().Bool("unsafe-local", false, "Allow reading encrypted secrets from local config (local backend)")
	return cmd
}

func runGetSecret(cmd *cobra.Command, args []string) error {
	backend := backendFromCmd(cmd)
	name := args[0]

	switch backend {
	case "nomad":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runNomadGet(cmd, cfg, name)
	case "vault":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runVaultGet(cmd, cfg, name)
	default:
		return runLocalGet(cmd, name)
	}
}

// ── list ──────────────────────────────────────────────────────────────────────

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret keys",
		Long: `List secret keys in the selected backend.

  local  — shows key names (add --unsafe-local to decrypt values)
  nomad  — lists Nomad Variables under abc/secrets/<namespace>/
  vault  — lists Vault KV v2 keys under secret/data/abc/<namespace>/`,
		Args: cobra.NoArgs,
		RunE: runListSecrets,
	}
	cmd.Flags().Bool("unsafe-local", false, "Decrypt and display values (local backend only, requires ABC_CRYPT_PASSWORD)")
	return cmd
}

func runListSecrets(cmd *cobra.Command, _ []string) error {
	backend := backendFromCmd(cmd)

	switch backend {
	case "nomad":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runNomadList(cmd, cfg)
	case "vault":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return runVaultList(cmd, cfg)
	default:
		return runLocalList(cmd)
	}
}

// ── delete ────────────────────────────────────────────────────────────────────

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE:  runDeleteSecret,
	}
	cmd.Flags().Bool("unsafe-local", false, "Allow deleting local secrets")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

func runDeleteSecret(cmd *cobra.Command, args []string) error {
	backend := backendFromCmd(cmd)
	name := args[0]

	switch backend {
	case "nomad":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if !confirmDelete(cmd, name) {
			return fmt.Errorf("deletion cancelled")
		}
		return runNomadDelete(cmd, cfg, name)
	case "vault":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if !confirmDelete(cmd, name) {
			return fmt.Errorf("deletion cancelled")
		}
		return runVaultDelete(cmd, cfg, name)
	default:
		return runLocalDelete(cmd, name)
	}
}

func confirmDelete(cmd *cobra.Command, name string) bool {
	yes, _ := cmd.Flags().GetBool("yes")
	if yes {
		return true
	}
	fmt.Fprintf(cmd.OutOrStderr(), "Delete secret %q? (y/n) ", name)
	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		return false
	}
	return response == "y" || response == "yes"
}

// ── ref ───────────────────────────────────────────────────────────────────────

func newRefCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ref <name>",
		Short: "Print the HCL template snippet to read a secret in a Nomad job",
		Long: `Print the Nomad job template snippet that reads the named secret at runtime.

  nomad backend — outputs a nomadVar HCL call (alloc identity reads the variable)
  vault backend — outputs a vault secret HCL call (requires vault stanza in job)

Examples:
  abc secrets ref db-password --backend nomad
  abc secrets ref db-password --backend vault`,
		Args: cobra.ExactArgs(1),
		RunE: runRef,
	}
}

func runRef(cmd *cobra.Command, args []string) error {
	name := args[0]
	backend := backendFromCmd(cmd)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ns := nomadSecretsNamespace(cmd, cfg)

	var ref string
	switch backend {
	case "vault":
		ref = vaultSecretRef(ns, name)
	default:
		ref = nomadSecretRef(ns, name)
	}
	fmt.Fprintln(cmd.OutOrStdout(), ref)
	return nil
}

// ── backend subcommand ────────────────────────────────────────────────────────

func newBackendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Backend management commands",
	}
	cmd.AddCommand(newBackendSetupCmd())
	return cmd
}

func newBackendSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Create the Nomad ACL policy that grants job allocations read access to abc/secrets/*",
		Long: `Creates (or updates) the "abc-secrets-alloc-read" Nomad ACL policy so that
job allocations using nomadVar template calls can read secrets at runtime.

Requires an admin Nomad token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			return runNomadBackendSetup(cmd, cfg)
		},
	}
}

// ── local backend helpers ─────────────────────────────────────────────────────

func runLocalSet(cmd *cobra.Command, key, value string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("set requires --unsafe-local flag (or use --backend nomad/vault)")
	}

	password := os.Getenv("ABC_CRYPT_PASSWORD")
	salt := os.Getenv("ABC_CRYPT_SALT")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	password, salt, err = resolveSecretCredentials(cmd, cfg, password, salt)
	if err != nil {
		return err
	}
	ctxName, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return err
	}
	if ctx.Secrets == nil {
		ctx.Secrets = map[string]string{}
	}
	encrypted, err := config.EncryptField(value, password, salt)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	ctx.Secrets[key] = encrypted
	cfg.Contexts[ctxName] = ctx
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q stored (local).\n", key)
	return nil
}

func runLocalGet(cmd *cobra.Command, key string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("get requires --unsafe-local flag (or use --backend nomad/vault)")
	}

	password := os.Getenv("ABC_CRYPT_PASSWORD")
	salt := os.Getenv("ABC_CRYPT_SALT")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	password, salt, err = resolveSecretCredentials(cmd, cfg, password, salt)
	if err != nil {
		return err
	}
	_, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return err
	}
	encrypted, ok := ctx.Secrets[key]
	if !ok {
		return fmt.Errorf("secret %q not found", key)
	}
	decrypted, err := config.DecryptField(encrypted, password, salt)
	if err != nil {
		return fmt.Errorf("decrypt secret: %w", err)
	}
	fmt.Fprint(cmd.OutOrStdout(), decrypted)
	return nil
}

func runLocalList(cmd *cobra.Command) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctxName, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return err
	}
	if len(ctx.Secrets) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets stored for context %q (local).\n", ctxName)
		return nil
	}

	if unsafeLocal {
		password := os.Getenv("ABC_CRYPT_PASSWORD")
		salt := os.Getenv("ABC_CRYPT_SALT")
		password, salt, err = resolveSecretCredentials(cmd, cfg, password, salt)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "KEY\tVALUE\n")
		for key, encrypted := range ctx.Secrets {
			decrypted, err := config.DecryptField(encrypted, password, salt)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStderr(), "Warning: could not decrypt %q: %v\n", key, err)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", key, decrypted)
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "SECRETS (local, context %q, %d keys):\n", ctxName, len(ctx.Secrets))
		for key := range ctx.Secrets {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", key)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "\nUse --unsafe-local to view decrypted values (requires ABC_CRYPT_PASSWORD)")
	}
	return nil
}

func runLocalDelete(cmd *cobra.Command, key string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("delete requires --unsafe-local flag (or use --backend nomad/vault)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctxName, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return err
	}
	if _, ok := ctx.Secrets[key]; !ok {
		return fmt.Errorf("secret %q not found", key)
	}

	if !confirmDelete(cmd, key) {
		return fmt.Errorf("deletion cancelled")
	}

	delete(ctx.Secrets, key)
	cfg.Contexts[ctxName] = ctx
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q deleted (local).\n", key)
	return nil
}

func resolveSecretCredentials(cmd *cobra.Command, cfg *config.Config, envPassword, envSalt string) (string, string, error) {
	passwordProvided := envPassword != ""
	saltProvided := envSalt != ""
	configChanged := false

	ctxName, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return "", "", err
	}

	if ctx.Crypt.Password != "" {
		if passwordProvided && envPassword != ctx.Crypt.Password {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: ABC_CRYPT_PASSWORD differs from config; using config value from ~/.abc/config.yaml\n")
		}
		envPassword = ctx.Crypt.Password
	} else if passwordProvided {
		ctx.Crypt.Password = envPassword
		configChanged = true
	}

	if ctx.Crypt.Salt != "" {
		if saltProvided && envSalt != ctx.Crypt.Salt {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: ABC_CRYPT_SALT differs from config; using config value from ~/.abc/config.yaml\n")
		}
		envSalt = ctx.Crypt.Salt
	} else if saltProvided {
		ctx.Crypt.Salt = envSalt
		configChanged = true
	}

	if configChanged {
		cfg.Contexts[ctxName] = ctx
		if err := cfg.Save(); err != nil {
			return "", "", fmt.Errorf("save config: %w", err)
		}
	}

	if envPassword == "" {
		return "", "", fmt.Errorf("ABC_CRYPT_PASSWORD not set and no crypt.password in config; run: abc secrets init --unsafe-local")
	}
	return envPassword, envSalt, nil
}
