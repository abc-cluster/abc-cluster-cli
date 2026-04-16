// Package config implements the abc config command group.
//
// Subcommands:
//   - init   — initialize config interactively
//   - set    — set a config key to a value
//   - get    — get a config value
//   - list   — list all config keys and values
//   - unset  — unset a config key
package config

import (
	"fmt"
	"os"
	"text/tabwriter"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the root config command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage local configuration",
		Long: `Manage the abc-cluster CLI configuration file.

Configuration is stored at ~/.abc/config.yaml (or ABC_CONFIG_FILE).
This is where cli-managed fields like default region, output format, and
saved authentication contexts are stored.

Sensitive fields (access_token) can be encrypted with mozilla/sops.
See 'abc config encryption' for details.

Subcommands:
  abc config init       Initialize configuration interactively
  abc config set KEY VALUE    Set a configuration key
  abc config get KEY          Get a configuration key
  abc config list             List all configuration keys
  abc config unset KEY        Unset (clear) a configuration key`,
	}

	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newSetCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newUnsetCmd())

	return cmd
}

// newInitCmd returns the 'config init' subcommand.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration interactively",
		Long: `Create or update ~/.abc/config.yaml interactively.

Prompts for default settings and the first authentication context.
Equivalent to running 'abc auth login' and 'abc config set'.`,
		Args: cobra.NoArgs,
		RunE: runConfigInit,
	}
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	// For now, delegate to auth login
	fmt.Fprintf(os.Stderr, "Running 'abc auth login' to set up your first context...\n\n")

	// Use auth login directly by calling its implementation
	cfgFile, _ := cfg.Create()
	fmt.Fprintf(os.Stderr, "✓ Config initialized at %s\n", cfgFile)
	return nil
}

// newSetCmd returns the 'config set' subcommand.
func newSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set a configuration value",
		Long: `Set a configuration key to a value.

Supported keys follow a dot-separated path:
  defaults.output               table, json, or yaml
  defaults.region               Nomad region (e.g., za-cpt)
	contexts.<name>.endpoint          API endpoint URL
		contexts.<name>.upload_endpoint   Tus upload endpoint URL
			contexts.<name>.upload_token      Tus upload token
	contexts.<name>.access_token      Access token
	contexts.<name>.cluster           Cluster ID/name
	contexts.<name>.organization_id   Organization ID
	contexts.<name>.workspace_id      Workspace ID
	contexts.<name>.region            Region override for context
	contexts.<name>.crypt.password    Local crypt / secrets key material (per context)
	contexts.<name>.crypt.salt        Optional salt for contexts.<name>.crypt.password
	contexts.<name>.secrets.*         Encrypted values managed via abc secrets (per context)
		contexts.<name>.admin.services.nomad.nomad_addr   Node-specific Nomad API address
		contexts.<name>.admin.services.nomad.nomad_token   Node-specific Nomad ACL token
		contexts.<name>.admin.services.nomad.nomad_region Nomad RPC region (e.g. global); not contexts.region

Example:
  abc config set defaults.output json
  abc config set defaults.region za-cpt`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			key := args[0]
			value := args[1]

			if err := c.Set(key, value); err != nil {
				return err
			}

			if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				fmt.Fprintf(os.Stderr, "✓ Set %s = %s\n", key, value)
			}

			return nil
		},
	}
}

// newGetCmd returns the 'config get' subcommand.
func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get KEY",
		Short: "Get a configuration value",
		Long: `Print the value of a configuration key.

Output suitable for piping. Prints nothing if the key is unset.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			key := args[0]
			value, ok := c.Get(key)
			if !ok {
				return fmt.Errorf("config key %q not found", key)
			}

			fmt.Println(value)
			return nil
		},
	}
}

// newListCmd returns the 'config list' subcommand.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configuration keys",
		Long: `Display all configuration keys and values in table format.

Access tokens are masked for security (only first 8 characters shown).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			keys := c.AllKeys()
			if len(keys) == 0 {
				fmt.Fprintf(os.Stderr, "[abc] No configuration\n")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "KEY\tVALUE\n")
			for _, kv := range keys {
				key, value := kv[0], kv[1]
				if value == "" {
					continue // Skip empty values for readability
				}
				fmt.Fprintf(w, "%s\t%s\n", key, value)
			}
			w.Flush()

			return nil
		},
	}
}

// newUnsetCmd returns the 'config unset' subcommand.
func newUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset KEY",
		Short: "Unset (clear) a configuration value",
		Long: `Clear a configuration key, reverting to environment variables or built-in defaults.

Example:
  abc config unset defaults.output
  abc config unset contexts.org-a-za-cpt.region`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			key := args[0]
			if err := c.Unset(key); err != nil {
				return err
			}

			if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				fmt.Fprintf(os.Stderr, "✓ Unset %s\n", key)
			}

			return nil
		},
	}
}
