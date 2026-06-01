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
	"context"
	"fmt"
	"net/http"
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

Default expiry: 7d (168h). Pass --expires for other durations using Go
duration syntax (e.g. 24h, 720h for 30 days).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHubClient()
			if err != nil {
				return err
			}

			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w (use Go duration: 24h, 168h for 7d, …)", expires, err)
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
	cmd.Flags().StringVar(&expires, "expires", "168h", "Go duration before token expires (default 168h = 7d; e.g. 24h, 720h for 30d)")
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
//
// Two modes, auto-detected:
//   - Laptop (Context B, the common case): no JUPYTERHUB_* env vars present.
//     Mints a hub token via abc-auth-svc /workbench/token using the active
//     context's access token as the bearer.
//   - In-slot (Context A): JUPYTERHUB_* env vars present (running in a
//     JupyterLab terminal). Mints directly via the hub admin API.
//
// `--cred-source mint` forces the broker path even when in-slot.
func newConnectCmd() *cobra.Command {
	var clientType string
	var name string
	var expires string
	var credSource string
	var project string
	var authEndpointOverride string
	var checkProxy bool

	cmd := &cobra.Command{
		Use:          "connect",
		Short:        "Mint a workbench token + print a ready-to-paste URL for VS Code / Positron / etc.",
		SilenceUsage: true,
		Long: `One command to attach an external Jupyter client (VS Code, Positron,
JupyterLab Desktop) to your remote workbench session — from your own machine.

It mints a JupyterHub user token (via abc-auth-svc, authenticated by your
active context's access token), then prints the connection URL formatted for
the chosen client. The token value is printed exactly once.

Run it from your laptop after 'abc auth login'. (If run inside a JupyterLab
terminal it uses the in-session admin token instead — same result.)

Clients:
  vscode          (default) VS Code Jupyter extension — "Existing Jupyter Server"
  positron        Positron — New Connection → Existing Jupyter Server
  jupyter-desktop JupyterLab Desktop — File → New Connection → URL
  raw             print the bare URL only (for scripts)

Examples:
  abc workbench connect
  abc workbench connect --client positron
  abc workbench connect --project my-analysis --client positron
  abc workbench connect --check-proxy        # verify the URL actually reaches the hub`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientType = strings.ToLower(strings.TrimSpace(clientType))
			if !isKnownClient(clientType) {
				return fmt.Errorf("unknown --client %q (use vscode, positron, jupyter-desktop, or raw)", clientType)
			}

			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w", expires, err)
			}
			if name == "" {
				name = defaultTokenName()
			}

			cfg, err := abccfg.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			actx := cfg.ActiveCtx()

			inSlot := os.Getenv("JUPYTERHUB_API_TOKEN") != "" &&
				os.Getenv("JUPYTERHUB_API_URL") != "" &&
				os.Getenv("JUPYTERHUB_USER") != ""

			useBroker := credSource == "mint" || !inSlot

			var tokenValue, slot, hub, tokenID, expiresStr string

			if useBroker {
				// ── Laptop path: mint via abc-auth-svc ──────────────────────
				bearer := strings.TrimSpace(actx.AccessToken)
				if bearer == "" && actx.Admin.Services.Nomad != nil {
					bearer = strings.TrimSpace(actx.Admin.Services.Nomad.Token)
				}
				if bearer == "" {
					return fmt.Errorf("no access token in the active context — run `abc auth login` first")
				}

				authEndpoint := strings.TrimSpace(authEndpointOverride)
				if authEndpoint == "" {
					authEndpoint, err = wbinternal.DeriveAuthEndpoint(actx.Endpoint)
					if err != nil {
						return fmt.Errorf("derive auth-svc endpoint from %q: %w (use --auth-endpoint to override)",
							actx.Endpoint, err)
					}
				}

				resp, err := wbinternal.MintHubToken(cmd.Context(), authEndpoint, bearer,
					wbinternal.MintHubTokenRequest{Note: name, ExpiresIn: int64(dur.Seconds())})
				if err != nil {
					return fmt.Errorf("mint workbench token: %w", err)
				}
				tokenValue = resp.Token
				slot = resp.Slot
				hub = resp.HubURL
				if hub == "" {
					hub = hubURL(actx)
				}
				tokenID = resp.ID
				expiresStr = resp.ExpiresAt
			} else {
				// ── In-slot path: mint via the hub admin token in the env ───
				client, err := resolveHubClient()
				if err != nil {
					return err
				}
				tok, err := client.CreateToken(name, dur, nil)
				if err != nil {
					return fmt.Errorf("create token: %w", err)
				}
				tokenValue = tok.Token
				slot = client.User
				hub = hubURL(actx)
				tokenID = tok.ID
				if tok.Expires != nil {
					expiresStr = *tok.Expires
				}
			}

			url := connectURLForClient(hub, slot, tokenValue, project, clientType)

			// Optional: probe the URL to detect forward_auth blocking.
			if checkProxy {
				if msg := probeWorkbench(cmd.Context(), hub, slot, tokenValue); msg != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), msg)
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, url)
			if clientType != "raw" {
				fmt.Fprintln(out)
				for _, line := range clientInstructions(clientType) {
					fmt.Fprintln(out, "  "+line)
				}
				fmt.Fprintln(out)
				if expiresStr != "" {
					fmt.Fprintf(out, "  Token: %s (id %s) — expires %s\n", name, truncate(tokenID, 10), expiresStr)
				} else {
					fmt.Fprintf(out, "  Token: %s (id %s) — expires in %s\n", name, truncate(tokenID, 10), dur)
				}
				fmt.Fprintf(out, "  Revoke later: abc workbench token revoke %s\n", name)
			}

			// Note: we deliberately do NOT print the forward_auth caveat on the
			// happy path — the Caddy @jupyter_token bypass is live on seedling.
			// Use --check-proxy to actively verify reachability (it prints a
			// specific, actionable warning only when the proxy is misconfigured).
			return nil
		},
	}
	cmd.Flags().StringVar(&clientType, "client", "vscode", "target client: vscode | positron | jupyter-desktop | raw")
	cmd.Flags().StringVar(&name, "name", "", "label for the generated token (default: derived from context)")
	cmd.Flags().StringVar(&expires, "expires", "168h", "Go duration before the token expires (default 168h = 7d)")
	cmd.Flags().StringVar(&credSource, "cred-source", "auto", "auto | mint (force minting via auth-svc even when in-slot)")
	cmd.Flags().StringVar(&project, "project", "", "open the workbench at this project under ~/projects/ (browser clients only)")
	cmd.Flags().StringVar(&authEndpointOverride, "auth-endpoint", "", "override the auth-svc URL (default: derived from the active context endpoint)")
	cmd.Flags().BoolVar(&checkProxy, "check-proxy", false, "probe the URL to detect forward_auth blocking before printing")
	return cmd
}

// isKnownClient reports whether c is a recognised --client value.
func isKnownClient(c string) bool {
	switch c {
	case "vscode", "positron", "jupyter-desktop", "raw":
		return true
	}
	return false
}

// connectURLForClient composes the connect URL. API clients (VS Code,
// Positron, JupyterLab Desktop) connect to the server root; the browser
// form (project path) is only meaningful for human browsers, so for
// API clients we keep the root URL even when --project is set.
func connectURLForClient(hub, slot, token, project, clientType string) string {
	base := strings.TrimRight(hub, "/") + "/user/" + slot + "/"
	// API clients ignore the lab tree path; only a browser benefits from it.
	if project != "" && clientType == "raw" {
		// raw is the scriptable form; respect the project path for a browser open.
		return base + "lab/tree/projects/" + project + "/?token=" + token
	}
	return base + "?token=" + token
}

// clientInstructions returns the per-client paste instructions.
func clientInstructions(clientType string) []string {
	switch clientType {
	case "vscode":
		return []string{
			"Paste the URL above into VS Code:",
			"  Command Palette → 'Jupyter: Specify Jupyter Server for Connections' → Existing",
		}
	case "positron":
		return []string{
			"Paste the URL above into Positron:",
			"  Connections pane → New Connection → 'Existing Jupyter Server' → paste URL",
			"  (or Command Palette → 'Jupyter: Specify Jupyter Server')",
		}
	case "jupyter-desktop":
		return []string{
			"Open with JupyterLab Desktop:",
			"  File → New Connection → paste the URL",
		}
	}
	return nil
}

// probeWorkbench does a quick GET against the hub's /user/<slot>/api endpoint
// with the token, returning a human-readable warning if forward_auth is
// blocking (302 to a login page) or the token is rejected. Empty string on
// success.
func probeWorkbench(ctx context.Context, hub, slot, token string) string {
	endpoint := strings.TrimRight(hub, "/") + "/user/" + slot + "/api"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "token "+token)
	// Don't follow redirects — a 302 is the signal we want to catch.
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("[abc] could not probe %s: %v (URL may still work)", endpoint, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return "" // good — the token reaches the hub
	case resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther:
		loc := resp.Header.Get("Location")
		return "[abc] WARNING: forward_auth is redirecting token requests (302 → " + loc + ").\n" +
			"       Your URL is correct but VS Code / Positron will likely fail.\n" +
			"       Operator fix needed: Caddy @auth_token bypass for Authorization: token requests.\n" +
			"       Workaround: open the workbench in your browser first on this machine."
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Sprintf("[abc] WARNING: hub rejected the token (HTTP %d). It may be expired or scoped wrong.", resp.StatusCode)
	default:
		return fmt.Sprintf("[abc] note: probe returned HTTP %d (URL may still work).", resp.StatusCode)
	}
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

// forwardAuthCaveat is a short tip printed by the in-slot `token create`
// path (which can't actively probe). On seedling the Caddy @jupyter_token
// bypass lets token requests through; if an external client still fails with
// a 302/401 the proxy on that cluster may lack the bypass.
func forwardAuthCaveat() string {
	return strings.TrimSpace(`
Tip: if an external client fails with 302/401, the cluster's workbench proxy
may not allow token-authenticated requests. Run 'abc workbench connect
--check-proxy' to diagnose.
`)
}
