package contextcmd

import (
	"fmt"
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

Contexts are stored in ~/.abc/config.yaml under contexts.<name>. A context may be
a full mapping, or a string alias (e.g. default: aither) that reuses another context.
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
		Long:  "List all saved contexts and highlight the active one.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			names := c.AllContextEntryNames()
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No contexts configured.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ACTIVE\tNAME\tALIAS OF\tCLUSTER\tORG\tWORKSPACE\tREGION\tENDPOINT\n")
			for _, name := range names {
				ctx, _ := c.ContextNamed(name)
				aliasOf := ""
				if t, ok := c.ContextAliases[name]; ok {
					aliasOf = t
				}
				active := ""
				if c.ActiveContext == name {
					active = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					active,
					name,
					aliasOf,
					ctx.Cluster,
					ctx.OrgID,
					ctx.WorkspaceID,
					ctx.Region,
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
			fmt.Fprintf(cmd.OutOrStdout(), "Endpoint: %s\n", ctx.Endpoint)
			if ctx.UploadEndpoint != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Upload endpoint: %s\n", ctx.UploadEndpoint)
			}
			if ctx.UploadToken != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Upload token: %s\n", maskToken(ctx.UploadToken))
			}
			if ctx.Cluster != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Cluster: %s\n", ctx.Cluster)
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
	var cluster string
	var organizationID string
	var workspaceID string
	var region string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new saved context",
		Long: `Add a new named context and make it active.

A context includes endpoint, upload endpoint, upload token, access token,
optional cluster and organization IDs, optional workspace ID, and optional region.

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

			c.SetContext(name, cfg.Context{
				Endpoint:       endpoint,
				UploadEndpoint: uploadEp,
				UploadToken:    uploadToken,
				AccessToken:    token,
				Cluster:        cluster,
				OrgID:          organizationID,
				WorkspaceID:    workspaceID,
				Region:         region,
			})

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
	cmd.Flags().StringVar(&cluster, "cluster", "", "Cluster ID/name")
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
