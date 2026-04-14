// Package secrets implements the "abc secrets" command group.
//
// Manages encrypted secrets stored in the config file using password-based
// encryption (mozilla/sops --unsafe mode). All write operations require the
// --unsafe flag and ABC_CRYPT_PASSWORD environment variable.
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

Secrets are encrypted locally using password-based encryption (--unsafe mode).
All operations require ABC_CRYPT_PASSWORD environment variable.

Examples:
  export ABC_CRYPT_PASSWORD="my-secret-passphrase"
  abc secrets set my-api-key "sk-1234567890abcdef" --unsafe
  abc secrets get my-api-key --unsafe
  abc secrets list
  abc secrets delete my-api-key --unsafe`,
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

Requires --unsafe flag and ABC_CRYPT_PASSWORD environment variable.

Examples:
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets set aws-access-key "AKIAIOSFODNN7EXAMPLE" --unsafe
  abc secrets set db-url "postgres://user:pass@localhost/db" --unsafe`,
		Args: cobra.ExactArgs(2),
		RunE: runSetSecret,
	}

	cmd.Flags().Bool("unsafe", false, "Allow writing encrypted secrets (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newGetCmd returns the "secrets get" subcommand.
func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Retrieve and decrypt a secret",
		Long: `Get a secret by key, decrypting it on output.

Requires --unsafe flag and ABC_CRYPT_PASSWORD environment variable.

Examples:
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets get aws-access-key --unsafe
  abc secrets get db-url --unsafe | xargs echo "DB URL:"`,
		Args: cobra.ExactArgs(1),
		RunE: runGetSecret,
	}

	cmd.Flags().Bool("unsafe", false, "Allow reading encrypted secrets (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newListCmd returns the "secrets list" subcommand.
func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all secret keys",
		Long: `List all secrets stored in the config file.

Without --unsafe: shows only key names.
With --unsafe: decrypts and displays all secrets (requires ABC_CRYPT_PASSWORD).

Examples:
  abc secrets list                           # List key names only
  export ABC_CRYPT_PASSWORD="passphrase"
  abc secrets list --unsafe                  # List with decrypted values`,
		Args: cobra.NoArgs,
		RunE: runListSecrets,
	}

	cmd.Flags().Bool("unsafe", false, "Decrypt and display secret values (requires ABC_CRYPT_PASSWORD)")

	return cmd
}

// newDeleteCmd returns the "secrets delete" subcommand.
func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a secret",
		Long: `Remove a secret from the config file.

Requires --unsafe flag.

Examples:
  abc secrets delete aws-access-key --unsafe
  abc secrets delete old-credential --unsafe`,
		Args: cobra.ExactArgs(1),
		RunE: runDeleteSecret,
	}

	cmd.Flags().Bool("unsafe", false, "Allow deleting secrets")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")

	return cmd
}

// runSetSecret handles "abc secrets set <key> <value> --unsafe"
func runSetSecret(cmd *cobra.Command, args []string) error {
	unsafe, _ := cmd.Flags().GetBool("unsafe")
	if !unsafe {
		return fmt.Errorf("set requires --unsafe flag")
	}

	password := os.Getenv("ABC_CRYPT_PASSWORD")
	if password == "" {
		return fmt.Errorf("ABC_CRYPT_PASSWORD not set")
	}

	key := args[0]
	value := args[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}

	// Encrypt the value
	salt := os.Getenv("ABC_CRYPT_SALT")
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

// runGetSecret handles "abc secrets get <key> --unsafe"
func runGetSecret(cmd *cobra.Command, args []string) error {
	unsafe, _ := cmd.Flags().GetBool("unsafe")
	if !unsafe {
		return fmt.Errorf("get requires --unsafe flag")
	}

	password := os.Getenv("ABC_CRYPT_PASSWORD")
	if password == "" {
		return fmt.Errorf("ABC_CRYPT_PASSWORD not set")
	}

	key := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	encrypted, ok := cfg.Secrets[key]
	if !ok {
		return fmt.Errorf("secret %q not found", key)
	}

	// Decrypt the value
	salt := os.Getenv("ABC_CRYPT_SALT")
	decrypted, err := config.DecryptField(encrypted, password, salt)
	if err != nil {
		return fmt.Errorf("decrypt secret: %w", err)
	}

	// Output to stdout (pipe-safe, no newline for compatibility with xargs etc)
	fmt.Fprint(cmd.OutOrStdout(), decrypted)
	return nil
}

// runListSecrets handles "abc secrets list [--unsafe]"
func runListSecrets(cmd *cobra.Command, args []string) error {
	unsafe, _ := cmd.Flags().GetBool("unsafe")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(cfg.Secrets) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets stored.\n")
		return nil
	}

	if unsafe {
		password := os.Getenv("ABC_CRYPT_PASSWORD")
		if password == "" {
			return fmt.Errorf("ABC_CRYPT_PASSWORD not set")
		}

		salt := os.Getenv("ABC_CRYPT_SALT")
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
		fmt.Fprintf(cmd.OutOrStdout(), "\nUse --unsafe to view decrypted values (requires ABC_CRYPT_PASSWORD)\n")
	}

	return nil
}

// runDeleteSecret handles "abc secrets delete <key> --unsafe"
func runDeleteSecret(cmd *cobra.Command, args []string) error {
	unsafe, _ := cmd.Flags().GetBool("unsafe")
	if !unsafe {
		return fmt.Errorf("delete requires --unsafe flag")
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
