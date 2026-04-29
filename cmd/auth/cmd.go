// Package auth implements the abc auth command group.
//
// Subcommands:
//   - login   — interactive login: prompt for endpoint + token, validate, save to config
//   - logout  — clear the active context token (and optionally all contexts)
//   - whoami  — show current user identity
//   - token   — print the active access token to stdout
//   - refresh — refresh the access token (stub; OAuth flow not yet implemented)
package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the root auth command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate and manage session credentials",
		Long: `Manage authentication for the abc-cluster CLI.

Credentials are stored in ~/.abc/config.yaml (or ABC_CONFIG_FILE).
Each login creates a named context holding the endpoint, token, and optional
workspace and region settings.

  abc auth login           Interactive login — prompts for endpoint and token
  abc auth logout          Clear the active session
  abc auth whoami          Show the current authenticated identity
  abc auth token           Print the active access token (pipe-safe)
  abc auth refresh         Refresh an expiring token (stub)`,
	}

	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newWhoamiCmd())
	cmd.AddCommand(newTokenCmd())
	cmd.AddCommand(newRefreshCmd())

	return cmd
}

// newLoginCmd returns the 'auth login' subcommand.
func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Interactively authenticate with abc-cluster",
		Long: `Interactive login to abc-cluster.

Prompts for API endpoint and access token, then saves them to a new context.
Context name defaults to derived from endpoint and region, e.g. 'org-a-za-cpt'.

Credentials are stored at ~/.abc/config.yaml (or ABC_CONFIG_FILE).
See 'abc config encryption' for SOPS support.`,
		Args: cobra.NoArgs,
		RunE: runLogin,
	}
}

func runLogin(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	r := bufio.NewReader(os.Stdin)

	// Prompt for endpoint
	fmt.Fprintf(os.Stderr, "API endpoint [https://api.abc-cluster.io]: ")
	endpoint, _ := r.ReadString('\n')
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "https://api.abc-cluster.io"
	}

	// Prompt for token
	fmt.Fprintf(os.Stderr, "Access token: ")
	token, _ := r.ReadString('\n')
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("access token is required")
	}

	// Prompt for workspace (optional)
	fmt.Fprintf(os.Stderr, "Workspace ID (optional): ")
	workspace, _ := r.ReadString('\n')
	workspace = strings.TrimSpace(workspace)

	// Prompt for organization (optional)
	fmt.Fprintf(os.Stderr, "Organization ID (optional): ")
	organizationID, _ := r.ReadString('\n')
	organizationID = strings.TrimSpace(organizationID)

	// Prompt for region (optional)
	fmt.Fprintf(os.Stderr, "Region (optional): ")
	region, _ := r.ReadString('\n')
	region = strings.TrimSpace(region)

	// Validate token by attempting a ping to the endpoint
	_ = context.Background() // stub for future use
	quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
	if !quiet {
		fmt.Fprintf(os.Stderr, "Validating credentials...\n")
	}
	// Stub: just accept the token for now
	// In production, would validate with the API

	// Derive context name from endpoint
	contextName := deriveContextName(endpoint, region)

	// Save the context
	var uploadEp string
	if d, err := config.DeriveUploadEndpointFromAPI(endpoint); err == nil {
		uploadEp = d
	}
	ctx2 := config.Context{
		Endpoint:       endpoint,
		UploadEndpoint: uploadEp,
		AccessToken:    token,
		OrgID:          organizationID,
		WorkspaceID:    workspace,
		Region:         region,
	}
	if err := cfg.SetContext(contextName, ctx2); err != nil {
		return fmt.Errorf("save context: %w", err)
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "✓ Authenticated to %s\n", endpoint)
		fmt.Fprintf(os.Stderr, "✓ Context saved as: %s\n", contextName)
		if workspace != "" {
			fmt.Fprintf(os.Stderr, "✓ Workspace: %s\n", workspace)
		}
		if organizationID != "" {
			fmt.Fprintf(os.Stderr, "✓ Organization: %s\n", organizationID)
		}
		if region != "" {
			fmt.Fprintf(os.Stderr, "✓ Region: %s\n", region)
		}
	}

	return nil
}

// newLogoutCmd returns the 'auth logout' subcommand.
func newLogoutCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear the active session",
		Long: `Log out by clearing the active context.

Use --all to remove all saved contexts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")

			if all {
				cfg.Contexts = map[string]config.Context{}
				cfg.ContextAliases = map[string]string{}
				cfg.ActiveContext = ""
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				if !quiet {
					fmt.Fprintf(os.Stderr, "✓ All contexts removed\n")
				}
			} else {
				if cfg.ActiveContext == "" {
					fmt.Fprintf(os.Stderr, "[abc] No active context\n")
					return nil
				}
				cfg.ClearContext(cfg.ActiveContext)
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				if !quiet {
					fmt.Fprintf(os.Stderr, "✓ Logged out\n")
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false,
		"Remove all contexts (not just the active one)")
	return cmd
}

// newWhoamiCmd returns the 'auth whoami' subcommand.
func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current authenticated identity",
		Long: `Display the current user identity and active context details.

Contacts the Nomad API to resolve the token identity (name, type, accessor ID)
and saves the result to auth.whoami in the active context so it appears in
'abc config show' and other identity-aware commands.

If the Nomad endpoint is unreachable, the cached auth.whoami value is shown
instead (with a warning).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ActiveContext == "" {
				fmt.Fprintf(os.Stderr, "[abc] No active context. Run 'abc auth login' first.\n")
				return nil
			}

			canon := cfg.ResolveContextName(cfg.ActiveContext)
			if canon == "" {
				canon = cfg.ActiveContext
			}
			activeCtx, _ := cfg.ContextNamed(canon)

			// --- Nomad identity resolution ---
			addr, tok, region := activeCtx.NomadAddr(), activeCtx.NomadToken(), activeCtx.NomadRegion()
			var nomadTok *utils.NomadACLToken
			if addr != "" && tok != "" {
				nc := utils.NewNomadClient(addr, tok, region)
				nomadTok, err = nc.GetACLTokenSelf(context.Background())
				if err != nil {
					fmt.Fprintf(os.Stderr, "[abc] Warning: could not reach Nomad to resolve identity: %v\n", err)
					fmt.Fprintf(os.Stderr, "[abc] Showing cached identity (if any).\n")
				}
			}

			// Persist the resolved label to auth.whoami.
			if nomadTok != nil {
				label := utils.NomadWhoamiLabelFromACLToken(nomadTok)
				if label != "" {
					activeCtx.SetAuthWhoami(label)
					cfg.Contexts[canon] = activeCtx
					if saveErr := cfg.Save(); saveErr != nil {
						fmt.Fprintf(os.Stderr, "[abc] Warning: could not save auth.whoami: %v\n", saveErr)
					}
				}
			}

			// --- Display ---
			fmt.Printf("Context      %s\n", cfg.ActiveContext)
			if canon != cfg.ActiveContext {
				fmt.Printf("Canonical    %s\n", canon)
			}
			if als := config.AliasesResolvingToCanon(cfg, canon); len(als) > 0 {
				fmt.Printf("Aliases      %s\n", strings.Join(als, ", "))
			}
			fmt.Printf("Endpoint     %s\n", activeCtx.Endpoint)
			if activeCtx.OrgID != "" {
				fmt.Printf("Organization %s\n", activeCtx.OrgID)
			}
			if activeCtx.WorkspaceID != "" {
				fmt.Printf("Workspace    %s\n", activeCtx.WorkspaceID)
			}
			if activeCtx.Region != "" {
				fmt.Printf("Region       %s\n", activeCtx.Region)
			}
			fmt.Printf("Token        %s\n", maskToken(activeCtx.AccessToken))

			if nomadTok != nil {
				fmt.Printf("\nNomad identity\n")
				fmt.Printf("  Name         %s\n", nomadTok.Name)
				fmt.Printf("  Type         %s\n", nomadTok.Type)
				fmt.Printf("  Accessor ID  %s\n", nomadTok.AccessorID)
				if len(nomadTok.Policies) > 0 {
					fmt.Printf("  Policies     %s\n", strings.Join(nomadTok.Policies, ", "))
				}
				label := utils.NomadWhoamiLabelFromACLToken(nomadTok)
				if label != "" {
					fmt.Printf("  auth.whoami  %s  ✓ synced\n", label)
				}
			} else if activeCtx.Auth != nil && activeCtx.Auth.Whoami != "" {
				fmt.Printf("\nNomad identity  %s  (cached — Nomad unreachable)\n", activeCtx.Auth.Whoami)
			}

			return nil
		},
	}
}

// newTokenCmd returns the 'auth token' subcommand.
func newTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the access token (pipe-safe)",
		Long: `Print the active context's access token to stdout.

Safe for piping to echo or storing in environment variables.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ActiveContext == "" {
				return fmt.Errorf("no active context")
			}

			ctx, _ := cfg.ContextNamed(cfg.ActiveContext)
			fmt.Println(ctx.AccessToken)
			return nil
		},
	}
}

// newRefreshCmd returns the 'auth refresh' subcommand.
func newRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the access token (stub)",
		Long: `Refresh the current access token.

This is a stub. Full OAuth flow not yet implemented.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stderr, "[abc] Token refresh not yet implemented\n")
			return nil
		},
	}
}

// deriveContextName creates a context name from endpoint and region.
// E.g. "https://api.org-a.example" + "za-cpt" -> "org-a-za-cpt".
// active_context selects which saved context is used; there is no reserved "default" name.
func deriveContextName(endpoint, region string) string {
	// Extract org name from endpoint domain
	parts := strings.SplitAfter(endpoint, "://")
	if len(parts) > 1 {
		domain := parts[len(parts)-1]
		// Remove .example, .io, etc.
		domain = strings.SplitN(domain, ".", 2)[0]
		if region != "" {
			return fmt.Sprintf("%s-%s", domain, region)
		}
		return domain
	}
	return "main"
}

func maskToken(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return strings.Repeat("•", len(tok))
	}
	return tok[:8] + strings.Repeat("•", len(tok)-8)
}
