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
	return &cobra.Command{
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

			name := c.ActiveContext
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return fmt.Errorf("no active context; specify a context name")
			}

			ctx, ok := c.ContextNamed(name)
			if !ok {
				return fmt.Errorf("context %q not found", name)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", name)
			if t, ok := c.ContextAliases[name]; ok {
				fmt.Fprintf(cmd.OutOrStdout(), "Alias of: %s\n", t)
			}
			canon := c.ResolveContextName(name)
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
				fmt.Fprintf(cmd.OutOrStdout(), "Upload token: %s\n", maskToken(ctx.UploadToken))
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
			fmt.Fprintf(cmd.OutOrStdout(), "Access token: %s\n", maskToken(ctx.AccessToken))
			if c.ActiveContext == name {
				fmt.Fprintln(cmd.OutOrStdout(), "Active: yes")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Active: no")
			}
			return nil
		},
	}
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

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new saved context",
		Long: `Add a new named context and make it active.

A context includes endpoint, upload endpoint, upload token, access token,
optional organization ID, optional workspace ID, and optional region.

If --upload-endpoint is omitted, it defaults to <endpoint>/files/ (no duplicate slashes).
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if _, def := c.Contexts[name]; def {
				return fmt.Errorf("context %q already exists", name)
			}
			if _, al := c.ContextAliases[name]; al {
				return fmt.Errorf("name %q is already a context alias", name)
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
