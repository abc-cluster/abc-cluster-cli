package workbench

// token.go — `abc workbench token …` and `abc workbench connect`.
//
// Wraps the JupyterHub user-tokens REST API so a slot user can create,
// list, revoke, and emit ready-to-paste URLs for external Jupyter clients
// (VS Code, JupyterLab Desktop, MCP servers, scripts) — without visiting
// the browser's File → Hub Control Panel → Token page.
//
// Authentication strategy ("Context A" — inside the slot's JupyterLab
// terminal): read JUPYTERHUB_API_TOKEN, JUPYTERHUB_API_URL, JUPYTERHUB_USER
// from the spawned environment (injected by SystemdSpawner). If absent
// (Context B — laptop CLI), exit with a helpful error pointing at the
// in-Lab terminal flow; the laptop-side flow is deferred (see brainstorm).
//
// Caveat: an external client using this URL must reach JupyterHub through
// the Caddy `forward_auth` proxy. Today forward_auth gates on the MinIO
// session cookie; a `?token=…` URL has no cookie. The CLI surfaces this
// caveat in the create / connect output so users aren't surprised.
//
// See brainstorms/abc-workbench/2026-06-01-workbench-token-cli.md.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
	wbinternal "github.com/abc-cluster/abc-cluster-cli/internal/workbench"
)

// newTokenCmd returns `abc workbench token` with its four subcommands.
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Create, list, revoke, or compose URLs for JupyterHub user tokens",
		Long: `Manage JupyterHub user tokens for connecting external clients
(VS Code, JupyterLab Desktop, MCP servers, scripts) to your workbench session.

Must be run from inside a JupyterLab terminal — the underlying authentication
uses the JUPYTERHUB_API_TOKEN injected into your slot's environment.

For the "I just want it to work" path, see: abc workbench connect`,
	}
	cmd.AddCommand(newTokenCreateCmd())
	cmd.AddCommand(newTokenListCmd())
	cmd.AddCommand(newTokenRevokeCmd())
	cmd.AddCommand(newTokenURLCmd())
	return cmd
}

func newTokenCreateCmd() *cobra.Command {
	var name string
	var expires string
	var scope []string

	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Generate a new JupyterHub user token (printed once)",
		SilenceUsage: true,
		Long: `Generate a new JupyterHub user token and print the URL that external
clients (VS Code, JupyterLab Desktop) paste to connect.

The token value is shown ONCE — JupyterHub does not return it later. Use
'abc workbench token list' to see what's active (metadata only), and
'abc workbench token revoke' to clean up.

Default expiry: 30d. Pass --expires for other durations using Go duration
syntax (e.g. 24h, 168h for 7 days, 720h for 30 days).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}

			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w (use Go duration: 24h, 168h for 7d, 720h for 30d)", expires, err)
			}
			if dur <= 0 {
				return fmt.Errorf("--expires must be positive")
			}

			if name == "" {
				name = defaultTokenName()
			}

			tok, err := client.CreateToken(name, dur, scope)
			if err != nil {
				return fmt.Errorf("create token: %w", err)
			}

			cfg, _ := abccfg.Load()
			hub := hubURL(cfg.ActiveCtx())
			url := connectURL(hub, client.User, tok.Token)

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Token created.")
			fmt.Fprintf(out, "  Name:    %s\n", name)
			fmt.Fprintf(out, "  ID:      %s\n", tok.ID)
			fmt.Fprintf(out, "  Token:   %s\n", tok.Token)
			if tok.Expires != nil && *tok.Expires != "" {
				fmt.Fprintf(out, "  Expires: %s\n", *tok.Expires)
			} else {
				fmt.Fprintf(out, "  Expires: in %s\n", dur)
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  URL:     %s\n", url)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  Paste the URL into VS Code:")
			fmt.Fprintln(out, "    Command Palette → 'Jupyter: Specify Jupyter Server for Connections' → Existing")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  Note: the token value is shown ONCE. Capture it now or revoke + recreate later.")
			fmt.Fprintln(cmd.ErrOrStderr())
			fmt.Fprintln(cmd.ErrOrStderr(), forwardAuthCaveat())
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "human-readable label (default: derived from invocation context)")
	cmd.Flags().StringVar(&expires, "expires", "720h", "Go duration before token expires (default 720h = 30d; e.g. 24h, 168h for 7d)")
	cmd.Flags().StringSliceVar(&scope, "scope", nil, "JupyterHub token scope(s); empty = default 'self'")
	return cmd
}

func newTokenListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List your active JupyterHub user tokens (metadata only)",
		SilenceUsage: true,
		Long: `List the active user tokens for your slot. JupyterHub does not return
token values after creation — the listing shows only metadata.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}
			toks, err := client.ListTokens()
			if err != nil {
				return fmt.Errorf("list tokens: %w", err)
			}
			if len(toks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No active tokens.")
				return nil
			}
			// Stable order: by created ascending so older tokens float to the top.
			sort.SliceStable(toks, func(i, j int) bool { return toks[i].Created < toks[j].Created })

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%-10s  %-30s  %-20s  %-20s  %s\n", "ID", "NAME", "CREATED", "EXPIRES", "LAST USED")
			fmt.Fprintln(out, strings.Repeat("─", 110))
			for _, t := range toks {
				expires := "(none)"
				if t.Expires != nil && *t.Expires != "" {
					expires = *t.Expires
				}
				last := "(never)"
				if t.LastActivity != nil && *t.LastActivity != "" {
					last = *t.LastActivity
				}
				fmt.Fprintf(out, "%-10s  %-30s  %-20s  %-20s  %s\n",
					truncate(t.ID, 10), truncate(t.Note, 30), t.Created, expires, last)
			}
			return nil
		},
	}
	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "revoke <id-or-name>",
		Short:        "Revoke a JupyterHub user token",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Long: `Revoke a token by its ID (or ID prefix) or by its name. If the name
matches multiple tokens, the operation fails — pass the ID prefix to
disambiguate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}
			toks, err := client.ListTokens()
			if err != nil {
				return fmt.Errorf("list tokens: %w", err)
			}
			arg := strings.TrimSpace(args[0])

			matches := findTokenMatches(toks, arg)
			switch len(matches) {
			case 0:
				return fmt.Errorf("no token matches %q (try `abc workbench token list`)", arg)
			case 1:
				t := matches[0]
				if err := client.RevokeToken(t.ID); err != nil {
					return fmt.Errorf("revoke %s: %w", t.ID, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Revoked: %s (id %s)\n", t.Note, truncate(t.ID, 10))
				return nil
			default:
				ids := make([]string, 0, len(matches))
				for _, m := range matches {
					ids = append(ids, truncate(m.ID, 10)+" ("+m.Note+")")
				}
				return fmt.Errorf("ambiguous: %d tokens match %q — disambiguate by ID prefix:\n  %s",
					len(matches), arg, strings.Join(ids, "\n  "))
			}
		},
	}
	return cmd
}

func newTokenURLCmd() *cobra.Command {
	var clientType string

	cmd := &cobra.Command{
		Use:          "url [<id-or-name>]",
		Short:        "Compose a connect URL for an existing token (you must already have the token value)",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		Long: `Compose a connect URL for an existing JupyterHub user token.

This command does NOT retrieve the token value — JupyterHub does not return
tokens after creation. Pass the token value via stdin or the ABC_HUB_TOKEN
env var; the command composes the URL with the right hub host + slot path.

Useful when you saved a token value previously and want the URL again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}

			tokenValue := strings.TrimSpace(envvars.Get("ABC_HUB_TOKEN"))
			if tokenValue == "" {
				return fmt.Errorf("set ABC_HUB_TOKEN to the token value, or use `abc workbench token create` to generate + print a new URL")
			}

			cfg, _ := abccfg.Load()
			hub := hubURL(cfg.ActiveCtx())
			url := connectURL(hub, client.User, tokenValue)

			switch clientType {
			case "vscode":
				fmt.Fprintln(cmd.OutOrStdout(), url)
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "  VS Code: Command Palette → 'Jupyter: Specify Jupyter Server for Connections' → Existing")
			case "jupyter-desktop", "raw", "":
				fmt.Fprintln(cmd.OutOrStdout(), url)
			default:
				return fmt.Errorf("unknown --client %q (use vscode, jupyter-desktop, or raw)", clientType)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&clientType, "client", "raw", "output format: vscode | jupyter-desktop | raw")
	return cmd
}

// newConnectCmd returns `abc workbench connect` — the porcelain.
func newConnectCmd() *cobra.Command {
	var clientType string
	var name string
	var expires string

	cmd := &cobra.Command{
		Use:          "connect",
		Short:        "Generate a token + emit a ready-to-paste URL for an external Jupyter client",
		SilenceUsage: true,
		Long: `One-shot: create a JupyterHub user token + print the connect URL formatted
for the chosen external client. The token value is printed exactly once.

Default --client is vscode; --client raw emits the bare URL only.

Run this from inside a JupyterLab terminal in your active workbench session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}

			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w", expires, err)
			}

			if name == "" {
				name = defaultTokenName()
			}
			tok, err := client.CreateToken(name, dur, nil)
			if err != nil {
				return fmt.Errorf("create token: %w", err)
			}

			cfg, _ := abccfg.Load()
			hub := hubURL(cfg.ActiveCtx())
			url := connectURL(hub, client.User, tok.Token)

			out := cmd.OutOrStdout()
			switch clientType {
			case "vscode":
				fmt.Fprintln(out, url)
				fmt.Fprintln(out)
				fmt.Fprintln(out, "  Paste the URL above into VS Code:")
				fmt.Fprintln(out, "    Command Palette → 'Jupyter: Specify Jupyter Server for Connections' → Existing")
				fmt.Fprintln(out)
				fmt.Fprintf(out, "  Token name: %s   ID: %s   Expires in: %s\n", name, truncate(tok.ID, 10), dur)
				fmt.Fprintln(out, "  Revoke later: abc workbench token revoke "+name)
			case "raw":
				fmt.Fprintln(out, url)
			case "jupyter-desktop":
				fmt.Fprintln(out, url)
				fmt.Fprintln(out, "  Open with JupyterLab Desktop (File → New Connection → URL).")
			default:
				return fmt.Errorf("unknown --client %q", clientType)
			}
			fmt.Fprintln(cmd.ErrOrStderr())
			fmt.Fprintln(cmd.ErrOrStderr(), forwardAuthCaveat())
			return nil
		},
	}
	cmd.Flags().StringVar(&clientType, "client", "vscode", "target client: vscode | jupyter-desktop | raw")
	cmd.Flags().StringVar(&name, "name", "", "label for the generated token (default: derived from context)")
	cmd.Flags().StringVar(&expires, "expires", "720h", "Go duration before the token expires (default 720h = 30d)")
	return cmd
}

// resolveHubClient builds a HubClient from JUPYTERHUB_* env vars (Context A:
// inside the slot's singleuser terminal, where SystemdSpawner has injected
// them). Returns a clear error when the env vars are missing (Context B:
// laptop CLI — not yet implemented).
func resolveHubClient() (*wbinternal.HubTokenClient, error) {
	apiURL := strings.TrimSpace(os.Getenv("JUPYTERHUB_API_URL"))
	token := strings.TrimSpace(os.Getenv("JUPYTERHUB_API_TOKEN"))
	user := strings.TrimSpace(os.Getenv("JUPYTERHUB_USER"))
	if apiURL == "" || token == "" || user == "" {
		return nil, fmt.Errorf(
			"this command must be run from inside a JupyterLab terminal in your active workbench session\n" +
				"(JUPYTERHUB_API_URL, JUPYTERHUB_API_TOKEN, JUPYTERHUB_USER must be set).\n\n" +
				"To open a terminal: log in at the workbench URL and use File → New → Terminal.")
	}
	return wbinternal.NewHubTokenClient(apiURL, token, user), nil
}

// connectURL composes the JupyterHub user-server URL with the ?token=… query.
func connectURL(hub, user, token string) string {
	return strings.TrimRight(hub, "/") + "/user/" + user + "/?token=" + token
}

// defaultTokenName derives a sensible label when the user didn't pass --name.
// Uses a timestamp so concurrent invocations don't collide on JupyterHub's
// non-unique note field.
func defaultTokenName() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "abc-cli"
	}
	return host + "-" + time.Now().Format("20060102-150405")
}

// findTokenMatches returns tokens whose ID has the given prefix OR whose
// note (name) equals the given string exactly.
func findTokenMatches(toks []wbinternal.HubToken, arg string) []wbinternal.HubToken {
	var byID, byName []wbinternal.HubToken
	for _, t := range toks {
		if strings.HasPrefix(t.ID, arg) {
			byID = append(byID, t)
		}
		if t.Note == arg {
			byName = append(byName, t)
		}
	}
	// Prefer ID-prefix matches (more specific). Fall through to name.
	if len(byID) > 0 {
		return byID
	}
	return byName
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// forwardAuthCaveat is the one-paragraph note we print to stderr after
// create / connect, so the user isn't surprised when VS Code can't reach
// the URL because of the forward_auth proxy.
func forwardAuthCaveat() string {
	return strings.TrimSpace(`
Note: the URL goes through Caddy's forward_auth proxy. If VS Code fails with
a 302/401, the proxy may be rejecting token-only requests (no MinIO session
cookie). Operator fix: add a Caddy @auth_token bypass mirroring the existing
@websocket pattern. See brainstorms/abc-workbench/2026-06-01-workbench-token-cli.md.
`)
}
