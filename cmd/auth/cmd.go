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
	ctx2 := config.Context{
		Endpoint:    endpoint,
		AccessToken: token,
		WorkspaceID: workspace,
		Region:      region,
	}
	cfg.SetContext(contextName, ctx2)

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "✓ Authenticated to %s\n", endpoint)
		fmt.Fprintf(os.Stderr, "✓ Context saved as: %s\n", contextName)
		if workspace != "" {
			fmt.Fprintf(os.Stderr, "✓ Workspace: %s\n", workspace)
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

This is a stub implementation that displays context information.
Full user details (name, role, plan, etc.) require API contact.`,
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

			ctx := cfg.Contexts[cfg.ActiveContext]
			fmt.Printf("Context      %s\n", cfg.ActiveContext)
			fmt.Printf("Endpoint     %s\n", ctx.Endpoint)
			if ctx.WorkspaceID != "" {
				fmt.Printf("Workspace    %s\n", ctx.WorkspaceID)
			}
			if ctx.Region != "" {
				fmt.Printf("Region       %s\n", ctx.Region)
			}
			fmt.Printf("Token        %s (first 8 chars)\n", maskToken(ctx.AccessToken))
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

			ctx := cfg.Contexts[cfg.ActiveContext]
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
// E.g. "https://api.org-a.example" + "za-cpt" -> "org-a-za-cpt"
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
	return "default"
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
