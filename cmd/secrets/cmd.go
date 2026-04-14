// Package secrets implements the "abc secrets" command group.
//
// Manages encrypted secrets stored in the config file using password-based
// encryption (mozilla/sops local encryption mode). All write operations require the
// --unsafe-local flag and ABC_CRYPT_PASSWORD environment variable.
//
// Schema (in ~/.abc/config.yaml):
//
//	secrets:
//	  my-api-key: |
//	    ENC[AES256_GCM,data:...,iv:...,tag:...,type:str]
//	  db-password: |
//	    ENC[AES256_GCM,data:...,iv:...,tag:...,type:str]
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
		Short: "Manage encrypted secrets stored in config file",
		Long: `Manage encrypted secrets without exposing credentials.

Secrets are encrypted locally using password-based encryption (local password mode).
All operations use password-based local encryption.
Values from ~/.abc/config.yaml take precedence; otherwise ABC_CRYPT_PASSWORD and optional ABC_CRYPT_SALT are used.

Examples:
  export ABC_CRYPT_PASSWORD="my-secret-passphrase"
  abc secrets set my-api-key "sk-1234567890abcdef" --unsafe-local
  abc secrets get my-api-key --unsafe-local
  abc secrets list
  abc secrets delete my-api-key --unsafe-local`,
	}

	cmd.AddCommand(
		newSetCmd(),
		newGetCmd(),
		newListCmd(),
		newDeleteCmd(),
	)

	return cmd
}

// newSetCmd returns the "secrets set" subcommand.
func newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Store an encrypted secret",
		Long: `Store a value as an encrypted secret in the config file.

Requires --unsafe-local flag. Values from ~/.abc/config.yaml take precedence; otherwise ABC_CRYPT_PASSWORD and optional ABC_CRYPT_SALT are used.

Examples:
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets set aws-access-key "AKIAIOSFODNN7EXAMPLE" --unsafe-local
  abc secrets set db-url "postgres://user:pass@localhost/db" --unsafe-local`,

		Args: cobra.ExactArgs(2),
		RunE: runSetSecret,
	}

	cmd.Flags().Bool("unsafe-local", false, "Allow writing encrypted secrets locally (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newGetCmd returns the "secrets get" subcommand.
func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Retrieve and decrypt a secret",
		Long: `Get a secret by key, decrypting it on output.

Requires --unsafe-local flag. Values from ~/.abc/config.yaml take precedence; otherwise ABC_CRYPT_PASSWORD and optional ABC_CRYPT_SALT are used.

Examples:
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets get aws-access-key --unsafe-local
  abc secrets get db-url --unsafe-local | xargs echo "DB URL:"`,

		Args: cobra.ExactArgs(1),
		RunE: runGetSecret,
	}

	cmd.Flags().Bool("unsafe-local", false, "Allow reading encrypted secrets locally (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newListCmd returns the "secrets list" subcommand.
func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all secret keys",
		Long: `List all secrets stored in the config file.

Without --unsafe-local: shows only key names.
With --unsafe-local: decrypts and displays all secrets (requires ABC_CRYPT_PASSWORD).

Examples:
  abc secrets list                           # List key names only
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets list --unsafe-local                  # List with decrypted values`,
		Args: cobra.NoArgs,
		RunE: runListSecrets,
	}

	cmd.Flags().Bool("unsafe-local", false, "Decrypt and display secret values locally (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newDeleteCmd returns the "secrets delete" subcommand.
func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a secret",
		Long: `Remove a secret from the config file.

Requires --unsafe-local flag.

Examples:
  abc secrets delete aws-access-key --unsafe-local
  abc secrets delete old-credential --unsafe-local`,
		Args: cobra.ExactArgs(1),
		RunE: runDeleteSecret,
	}

	cmd.Flags().Bool("unsafe-local", false, "Allow deleting secrets")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")

	return cmd
}

// runSetSecret handles "abc secrets set <key> <value> --unsafe-local"
func runSetSecret(cmd *cobra.Command, args []string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("set requires --unsafe-local flag")
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

	key := args[0]
	value := args[1]

	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}

	// Encrypt the value
	encrypted, err := config.EncryptField(value, password, salt)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	cfg.Secrets[key] = encrypted

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q stored.\n", key)
	return nil
}

// runGetSecret handles "abc secrets get <key> --unsafe-local"
func runGetSecret(cmd *cobra.Command, args []string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("get requires --unsafe-local flag")
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

	key := args[0]

	encrypted, ok := cfg.Secrets[key]
	if !ok {
		return fmt.Errorf("secret %q not found", key)
	}

	// Decrypt the value
	decrypted, err := config.DecryptField(encrypted, password, salt)
	if err != nil {
		return fmt.Errorf("decrypt secret: %w", err)
	}

	// Output to stdout (pipe-safe, no newline for compatibility with xargs etc)
	fmt.Fprint(cmd.OutOrStdout(), decrypted)
	return nil
}

// runListSecrets handles "abc secrets list [--unsafe-local]"
func runListSecrets(cmd *cobra.Command, args []string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(cfg.Secrets) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets stored.\n")
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
		for key, encrypted := range cfg.Secrets {
			decrypted, err := config.DecryptField(encrypted, password, salt)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStderr(), "Warning: could not decrypt %q: %v\n", key, err)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", key, decrypted)
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "SECRETS (%d):\n", len(cfg.Secrets))
		for key := range cfg.Secrets {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", key)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nUse --unsafe-local to view decrypted values (requires ABC_CRYPT_PASSWORD)\n")
	}

	return nil
}

// runDeleteSecret handles "abc secrets delete <key> --unsafe-local"
func runDeleteSecret(cmd *cobra.Command, args []string) error {
	unsafeLocal, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafeLocal {
		return fmt.Errorf("delete requires --unsafe-local flag")
	}

	key := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if _, ok := cfg.Secrets[key]; !ok {
		return fmt.Errorf("secret %q not found", key)
	}

	// Confirm deletion
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		fmt.Fprintf(cmd.OutOrStderr(), "Delete secret %q? (y/n) ", key)
		var response string
		if _, err := fmt.Scanln(&response); err != nil || (response != "y" && response != "yes") {
			return fmt.Errorf("deletion cancelled")
		}
	}

	delete(cfg.Secrets, key)

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q deleted.\n", key)
	return nil
}

func resolveSecretCredentials(cmd *cobra.Command, cfg *config.Config, envPassword, envSalt string) (string, string, error) {
	passwordProvided := envPassword != ""
	saltProvided := envSalt != ""
	configChanged := false

	if cfg.Defaults.CryptPassword != "" {
		if passwordProvided && envPassword != cfg.Defaults.CryptPassword {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: ABC_CRYPT_PASSWORD differs from config; using config value from ~/.abc/config.yaml\n")
		}
		envPassword = cfg.Defaults.CryptPassword
	} else if passwordProvided {
		cfg.Defaults.CryptPassword = envPassword
		configChanged = true
	}

	if cfg.Defaults.CryptSalt != "" {
		if saltProvided && envSalt != cfg.Defaults.CryptSalt {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: ABC_CRYPT_SALT differs from config; using config value from ~/.abc/config.yaml\n")
		}
		envSalt = cfg.Defaults.CryptSalt
	} else if saltProvided {
		cfg.Defaults.CryptSalt = envSalt
		configChanged = true
	}

	if configChanged {
		if err := cfg.Save(); err != nil {
			return "", "", fmt.Errorf("save config: %w", err)
		}
	}

	if envPassword == "" {
		return "", "", fmt.Errorf("ABC_CRYPT_PASSWORD not set and no crypt password stored in config; set ABC_CRYPT_PASSWORD or add crypt_password to ~/.abc/config.yaml")
	}

	return envPassword, envSalt, nil
}
