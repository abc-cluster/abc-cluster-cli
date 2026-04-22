// Package config implements the abc config command group.
//
// Subcommands:
//   - init   — create config file and seed a "default" context
//   - set    — set a config key to a value
//   - get    — get a config value
//   - list   — list all config keys and values
//   - unset  — unset a config key
package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
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
saved authentication contexts are stored. First-time "abc config init" writes
a "default" context entry you can fill with "abc auth login" or "abc context add".

Sensitive fields (access_token) can be encrypted with mozilla/sops.
See 'abc config encryption' for details.

Subcommands:
  abc config init       Create config file and seed a placeholder "default" context
  abc config set KEY VALUE    Set a configuration key
  abc config get KEY          Get a configuration key
  abc config list             List all configuration keys
  abc config unset KEY        Unset (clear) a configuration key
  abc config fmt              Validate and rewrite config with sorted keys`,
	}

	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newSetCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newUnsetCmd())
	cmd.AddCommand(newFmtCmd())

	return cmd
}

// newInitCmd returns the 'config init' subcommand.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create config file and seed a placeholder \"default\" context",
		Long: `Create or update ~/.abc/config.yaml.

Ensures the file exists and seeds a placeholder context named "default" (public
API endpoint preset) with active_context set to "default" when none is set.
Use "abc auth login" or "abc context add" to fill in tokens and workspace fields.`,
		Args: cobra.NoArgs,
		RunE: runConfigInit,
	}
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	cfgFile, err := cfg.Create()
	if err != nil {
		return fmt.Errorf("config init: %w", err)
	}
	c, err := cfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Config ready at %s\n", cfgFile)
	if ac := strings.TrimSpace(c.ActiveContext); ac != "" {
		fmt.Fprintf(os.Stderr, "✓ Active context: %s (edit with: abc auth login  or  abc context add ...)\n", ac)
	} else {
		fmt.Fprintf(os.Stderr, "✓ Placeholder context %q added; pick active with: abc context use <name>\n", cfg.DefaultContextName)
	}
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
		contexts.<name>.upload_endpoint          Tus upload endpoint URL (direct Nomad tusd when synced)
			contexts.<name>.upload_token      Tus upload token
	contexts.<name>.access_token      Access token
	contexts.<name>.cluster_type      Platform tier (abc-nodes | abc-cluster | abc-cloud)
	contexts.<name>.aliases           Comma-separated alternate names for abc context use
	contexts.<name>.organization_id   Organization ID
	contexts.<name>.workspace_id      Workspace ID
	contexts.<name>.region            Region override for context
	contexts.<name>.crypt.password    Local crypt / secrets key material (per context)
	contexts.<name>.crypt.salt        Optional salt for contexts.<name>.crypt.password
	contexts.<name>.secrets.*         Encrypted values managed via abc secrets (per context)
		contexts.<name>.admin.services.nomad.nomad_addr   Nomad HTTP API base URL; for http:// include an explicit :PORT (same as other admin.services URLs)
		contexts.<name>.admin.services.nomad.nomad_token   Node-specific Nomad ACL token (updates auth.whoami from Nomad when reachable)
		contexts.<name>.admin.services.nomad.nomad_region Nomad RPC region (e.g. global); not contexts.region
		contexts.<name>.auth.whoami                         Nomad operator label (usually auto-filled from the token via Nomad)
		contexts.<name>.auth.root                           When true, marks a Nomad bootstrap / management-token context; YAML shorthand auth: root is accepted on load
		contexts.<name>.admin.whoami                        Optional persona label; for abc-nodes, Nomad namespace can be derived from a _admin / _researcher / … suffix (e.g. su-mbhg-bioinformatics_admin → namespace su-mbhg-bioinformatics)
		contexts.<name>.admin.abc_nodes.nomad_namespace    Explicit Nomad namespace for abc-nodes (overrides admin.whoami derivation; NOMAD_NAMESPACE when unset in env)
		contexts.<name>.admin.abc_nodes.s3_access_key     S3 access key (abc-nodes floor; merged into mc/rustfs env if unset)
		contexts.<name>.admin.abc_nodes.s3_secret_key     S3 secret key
		contexts.<name>.admin.abc_nodes.s3_region           AWS_DEFAULT_REGION
		contexts.<name>.admin.services.minio.endpoint       MinIO S3 API base URL — Nomad dynamic port when synced (mc; AWS_ENDPOINT_URL / AWS_ENDPOINT_URL_S3)
		contexts.<name>.admin.services.minio.access_key     Optional; overrides abc_nodes S3 keys for mc when set
		contexts.<name>.admin.services.minio.secret_key     Optional; paired with access_key
		contexts.<name>.admin.services.<svc>.user|password  Optional web UI creds per floor service (e.g. grafana); preserved by config sync
		contexts.<name>.admin.services.rustfs.endpoint      RustFS S3 API base URL (rustfs CLI; AWS_*)
		contexts.<name>.admin.services.rustfs.access_key    Optional; overrides abc_nodes for rustfs CLI when set
		contexts.<name>.admin.services.rustfs.secret_key    Optional; paired with rustfs access_key
		contexts.<name>.admin.services.rustfs.http          RustFS web console URL (browser login; config sync from Nomad console port)
		contexts.<name>.admin.services.faasd.http           faasd/OpenFaaS gateway URL (Nomad job abc-nodes-faasd, port http; used by admin health + config sync)
		contexts.<name>.admin.services.grafana_alloy.http   Grafana Alloy UI (Nomad job abc-nodes-alloy, port ui; config sync)
		contexts.<name>.admin.services.vault.http           Vault API base URL (Nomad job abc-nodes-vault, port http; merged into VAULT_ADDR for vault cli)
		contexts.<name>.admin.services.vault.access_key     Optional; merged into VAULT_TOKEN for vault cli; config sync sets from Nomad job when VAULT_DEV_ROOT_TOKEN_ID is in the abc-nodes-vault spec (lab -dev)
		contexts.<name>.admin.services.traefik.http             Traefik dashboard base URL (Nomad sync; also used by Traefik CLI for API/healthcheck when Traefik job is running)
		contexts.<name>.admin.services.traefik.endpoint         Traefik web entrypoint base URL (Nomad port http, usually :80)
		contexts.<name>.admin.services.traefik.ping_entrypoint  Optional; entry point name for traefik healthcheck snippets (default traefik)
		contexts.<name>.admin.abc_nodes.s3_endpoint         Deprecated alias for admin.services.minio.endpoint
		contexts.<name>.admin.abc_nodes.minio_root_user     MinIO root user (fallback for s3_access_key; MINIO_ROOT_USER)
		contexts.<name>.admin.abc_nodes.minio_root_password MinIO root password (fallback for s3_secret_key; MINIO_ROOT_PASSWORD)

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
			trySyncNomadWhoami(cmd, c, key)

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

// trySyncNomadWhoami fills contexts.<name>.auth.whoami after nomad_token is set, using Nomad GET /v1/acl/token/self.
func trySyncNomadWhoami(cmd *cobra.Command, c *cfg.Config, setKey string) {
	parts := strings.Split(setKey, ".")
	if len(parts) != 6 || parts[0] != "contexts" || parts[2] != "admin" || parts[3] != "services" || parts[4] != "nomad" || parts[5] != "nomad_token" {
		return
	}
	ctxName := parts[1]
	canon := c.ResolveContextName(ctxName)
	if canon == "" {
		return
	}
	ctx := c.Contexts[canon]
	addr, tok, region := ctx.NomadAddr(), ctx.NomadToken(), ctx.NomadRegion()
	if addr == "" || tok == "" {
		return
	}
	nc := utils.NewNomadClient(addr, tok, region)
	label, err := utils.NomadTokenWhoamiLabel(context.Background(), nc)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: could not resolve Nomad ACL identity (auth.whoami): %v\n", err)
		return
	}
	if label == "" {
		return
	}
	ctx.SetAuthWhoami(label)
	c.Contexts[canon] = ctx
}
