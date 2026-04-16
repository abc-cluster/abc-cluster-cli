package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate crypt password/salt for the active context (local)",
		Long: `Generate contexts.<name>.crypt.password and crypt.salt in ~/.abc/config.yaml
for the active (or sole) context when they are not already set. Use --force to replace existing values.

Requires --unsafe-local.`,
		Args: cobra.NoArgs,
		RunE: runInitCrypt,
	}

	cmd.Flags().Bool("unsafe-local", false, "Allow writing crypt defaults to local config")
	cmd.Flags().Bool("force", false, "Regenerate password/salt even if already present in config")

	return cmd
}

func runInitCrypt(cmd *cobra.Command, _ []string) error {
	unsafe, _ := cmd.Flags().GetBool("unsafe-local")
	if !unsafe {
		return fmt.Errorf("init requires --unsafe-local")
	}
	force, _ := cmd.Flags().GetBool("force")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctxName, ctx, err := cfg.ContextForSecrets()
	if err != nil {
		return err
	}

	if ctx.Crypt.Password != "" && ctx.Crypt.Salt != "" && !force {
		fmt.Fprintln(cmd.OutOrStdout(), "Crypt password and salt already set for this context; nothing to do. Use --force to regenerate.")
		return nil
	}

	if !force && ctx.Crypt.Password != "" {
		return fmt.Errorf("crypt.password is set but crypt.salt is missing; set salt manually or use --force to regenerate both")
	}
	if !force && ctx.Crypt.Salt != "" {
		return fmt.Errorf("crypt.salt is set but crypt.password is missing; set password manually or use --force to regenerate both")
	}

	passBytes := make([]byte, 32)
	if _, err := rand.Read(passBytes); err != nil {
		return fmt.Errorf("generate password: %w", err)
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}

	ctx.Crypt.Password = base64.RawURLEncoding.EncodeToString(passBytes)
	ctx.Crypt.Salt = base64.RawURLEncoding.EncodeToString(saltBytes)
	cfg.Contexts[ctxName] = ctx

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Generated contexts.%s.crypt.password and crypt.salt in config.\n", ctxName)
	fmt.Fprintln(cmd.OutOrStdout(), "You can now use abc secrets and abc data encrypt/decrypt without exporting ABC_CRYPT_PASSWORD.")
	return nil
}
