package contextcmd

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the context command group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage saved authentication contexts",
		Long: `Manage named authentication contexts for switching between clusters,
orgs, workspaces, and regions.

Contexts are stored in ~/.abc/config.yaml under contexts.<name>. A full context
may list aliases: or singular alias: for extra names you can pass to abc context use.
A top-level string entry (e.g. primary: aither) is still supported as a redirect.
The active context controls which endpoint, token, cluster, org, workspace, and region the CLI uses.
`,
	}

	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newUseCmd())
	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newDeleteCmd())

	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved contexts",
		Long:  "List primary context names, alternate names (aliases), and endpoint. Use 'abc context show' for full fields.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if len(c.Contexts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No contexts configured.")
				return nil
			}

			names := make([]string, 0, len(c.Contexts))
			for n := range c.Contexts {
				names = append(names, n)
			}
			sort.Strings(names)

			activeCanon := ""
			if strings.TrimSpace(c.ActiveContext) != "" {
				activeCanon = c.ResolveContextName(c.ActiveContext)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ACTIVE\tNAME\tALIASES\tENDPOINT\n")
			for _, name := range names {
				ctx := c.Contexts[name]
				aliasesCol := strings.Join(cfg.AliasesResolvingToCanon(c, name), ",")
				active := ""
				if activeCanon != "" && activeCanon == name {
					active = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					active,
					name,
					aliasesCol,
					ctx.Endpoint)
			}
			return w.Flush()
		},
	}
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show details for a context",
		Long: `Show details for the named context. If no name is provided, shows the active context.
`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			name, canon, ctx, err := resolveContextForShow(c, args)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", name)
			if t, ok := c.ContextAliases[name]; ok {
				fmt.Fprintf(cmd.OutOrStdout(), "Alias of: %s\n", t)
			}
			if canon != "" && canon != name {
				fmt.Fprintf(cmd.OutOrStdout(), "Canonical: %s\n", canon)
			}
			if canon != "" {
				if als := cfg.AliasesResolvingToCanon(c, canon); len(als) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Aliases: %s\n", strings.Join(als, ", "))
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Endpoint: %s\n", ctx.Endpoint)
			if ctx.UploadEndpoint != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Upload endpoint: %s\n", ctx.UploadEndpoint)
			}
			if ctx.UploadToken != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Upload token: %s\n", ctx.UploadToken)
			}
			if ctx.OrgID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Organization: %s\n", ctx.OrgID)
			}
			if ctx.WorkspaceID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", ctx.WorkspaceID)
			}
			if ctx.Region != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Region: %s\n", ctx.Region)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Access token: %s\n", ctx.AccessToken)
			if c.ActiveContext == name {
				fmt.Fprintln(cmd.OutOrStdout(), "Active: yes")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Active: no")
			}
			return nil
		},
	}
	cmd.AddCommand(newShowYAMLCmd())
	return cmd
}

func newShowYAMLCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "yaml [name]",
		Short: "Print a context as shareable YAML",
		Long: `Print a complete YAML snippet for the selected context.
This output includes sensitive values (access/upload tokens, secrets if present),
so share carefully.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			_, canon, ctx, err := resolveContextForShow(c, args)
			if err != nil {
				return err
			}

			aliases := cfg.AliasesResolvingToCanon(c, canon)
			if len(aliases) > 0 {
				ctx.Aliases = aliases
			}

			exportCfg := &cfg.Config{
				Version:       c.Version,
				ActiveContext: canon,
				Contexts: map[string]cfg.Context{
					canon: ctx,
				},
				ContextAliases: map[string]string{},
				Defaults:       c.Defaults,
			}
			for _, alias := range aliases {
				exportCfg.ContextAliases[alias] = canon
			}

			out, err := exportCfg.MarshalDocumentYAML()
			if err != nil {
				return fmt.Errorf("marshal yaml: %w", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}

func resolveContextForShow(c *cfg.Config, args []string) (name string, canon string, ctx cfg.Context, err error) {
	name = c.ActiveContext
	if len(args) == 1 {
		name = args[0]
	}
	if name == "" {
		return "", "", cfg.Context{}, fmt.Errorf("no active context; specify a context name")
	}

	var ok bool
	ctx, ok = c.ContextNamed(name)
	if !ok {
		return "", "", cfg.Context{}, fmt.Errorf("context %q not found", name)
	}

	canon = c.ResolveContextName(name)
	if canon == "" {
		canon = name
	}
	return name, canon, ctx, nil
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active context",
		Long:  "Set the active context to an existing saved context.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if !c.HasDefinedContext(name) {
				return fmt.Errorf("context %q not found", name)
			}

			c.ActiveContext = name
			if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Switched active context to %s\n", name)
			return nil
		},
	}
}

func newAddCmd() *cobra.Command {
	var endpoint string
	var uploadEndpoint string
	var uploadToken string
	var token string
	var organizationID string
	var workspaceID string
	var region string
	var fromFile string
	var asName string
	var force bool

	cmd := &cobra.Command{
		Use:   "add <name> | --from-file <path.yaml>",
		Short: "Add a new saved context",
		Long: `Add a new named context and make it active.

Flag-based form (name + explicit values):
  abc auth context add <name> --endpoint <url> --access-token <token> [...]

File-based form (import from a config YAML snippet):
  abc auth context add --from-file <path.yaml> [--as <name>] [--force]

The file form reads any YAML file in the standard abc config format (the output
of 'abc auth context show yaml') and imports its contexts into the current
~/.abc/config.yaml.  All sub-fields are preserved: admin.services,
capabilities, cluster_type, etc.

--as renames the active_context from the file to a different name on import.
--force overwrites an existing context of the same name.

Flag-based: if --upload-endpoint is omitted it defaults to <endpoint>/files/.
`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {

			// ── File-based import ────────────────────────────────────────────
			if fromFile != "" {
				src, err := cfg.LoadFrom(fromFile)
				if err != nil {
					return fmt.Errorf("load %q: %w", fromFile, err)
				}
				if len(src.Contexts) == 0 {
					return fmt.Errorf("%q contains no contexts", fromFile)
				}

				c, err := cfg.Load()
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}

				// Determine which context(s) to import.
				// If --as is set, rename the active_context from the file.
				srcActive := strings.TrimSpace(src.ActiveContext)
				if srcActive == "" {
					// Fall back to the first (and usually only) context in the file.
					for n := range src.Contexts {
						srcActive = n
						break
					}
				}

				added := 0
				for srcName, srcCtx := range src.Contexts {
					targetName := srcName
					if asName != "" && srcName == srcActive {
						targetName = asName
					}
					if _, exists := c.Contexts[targetName]; exists && !force {
						return fmt.Errorf(
							"context %q already exists; use --force to overwrite or --as to rename",
							targetName,
						)
					}
					if _, isAlias := c.ContextAliases[targetName]; isAlias && !force {
						return fmt.Errorf(
							"name %q is already a context alias; use --force to overwrite",
							targetName,
						)
					}
					if err := c.SetContext(targetName, srcCtx); err != nil {
						return fmt.Errorf("set context %q: %w", targetName, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Imported context %q\n", targetName)
					added++
				}

				// Make the (renamed) active context from the file the active context.
				active := srcActive
				if asName != "" {
					active = asName
				}
				if c.HasDefinedContext(active) {
					c.ActiveContext = active
				}

				if err := c.Save(); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				if added == 1 {
					fmt.Fprintf(cmd.OutOrStdout(), "Active context set to %q\n", c.ActiveContext)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Imported %d contexts; active context set to %q\n",
						added, c.ActiveContext)
				}
				return nil
			}

			// ── Flag-based add ────────────────────────────────────────────────
			if len(args) == 0 {
				return fmt.Errorf("a context name is required (or use --from-file <path.yaml>)")
			}
			name := args[0]
			if endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}
			if token == "" {
				return fmt.Errorf("--access-token is required")
			}

			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if _, def := c.Contexts[name]; def && !force {
				return fmt.Errorf("context %q already exists; use --force to overwrite", name)
			}
			if _, al := c.ContextAliases[name]; al && !force {
				return fmt.Errorf("name %q is already a context alias; use --force to overwrite", name)
			}

			uploadEp := strings.TrimSpace(uploadEndpoint)
			if uploadEp == "" {
				derived, err := cfg.DeriveUploadEndpointFromAPI(endpoint)
				if err != nil {
					return fmt.Errorf("derive upload endpoint from --endpoint: %w", err)
				}
				uploadEp = derived
			}

			if err := c.SetContext(name, cfg.Context{
				Endpoint:       endpoint,
				UploadEndpoint: uploadEp,
				UploadToken:    uploadToken,
				AccessToken:    token,
				OrgID:          organizationID,
				WorkspaceID:    workspaceID,
				Region:         region,
			}); err != nil {
				return fmt.Errorf("set context: %w", err)
			}

			if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added and activated context %q\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "API endpoint URL")
	cmd.Flags().StringVar(&uploadEndpoint, "upload-endpoint", "", "Tus upload endpoint URL (default: <endpoint>/files/)")
	cmd.Flags().StringVar(&uploadToken, "upload-token", "", "Tus upload token")
	cmd.Flags().StringVar(&token, "access-token", "", "API access token")
	cmd.Flags().StringVar(&organizationID, "organization-id", "", "Organization ID")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID")
	cmd.Flags().StringVar(&region, "region", "", "Region")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "Import context(s) from a config YAML file (output of 'abc auth context show yaml')")
	cmd.Flags().StringVar(&asName, "as", "", "Rename the active context from the file to this name on import (used with --from-file)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing context of the same name")

	return cmd
}

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved context",
		Long:  "Remove a saved context from the config file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if !c.HasDefinedContext(name) {
				return fmt.Errorf("context %q not found", name)
			}

			c.ClearContext(name)
			if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted context %q\n", name)
			return nil
		},
	}
}

func maskToken(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return strings.Repeat("•", len(tok))
	}
	return tok[:8] + strings.Repeat("•", 12)
}
