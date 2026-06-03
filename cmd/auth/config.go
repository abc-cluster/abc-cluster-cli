package auth

// config.go — `abc auth config` subverb (and its `refresh` child).
//
// `abc auth config refresh` re-pulls the rendered config blob for the active
// slot from the cluster's auth-svc (today: abc-auth-svc on seedling), replaces
// the active context in ~/.abc/config.yaml with what the server returns, and
// preserves any other contexts in the file.
//
// Use this after an admin rotates your credentials, on a fresh laptop, or to
// recover from a local-edit mishap.
//
// Auth: the active context's access_token (the slot's Nomad token) is sent as
// `Authorization: Bearer <token>` to GET /slots/me/config; abc-auth-svc looks
// up the slot by token name and returns the cached `config_yaml` blob.
//
// Design: brainstorms/abc-seedling-onboarding/2026-06-01-claim-time-config-dropthrough.md

import (
	"fmt"
	"io"
	"net/http"

	"net/url"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the local abc CLI config",
		Long: `Subcommands that operate on the local ~/.abc/config.yaml.

Today:
  refresh   Re-pull the active slot's config from the cluster auth service.`,
	}
	cmd.AddCommand(newConfigRefreshCmd())
	return cmd
}

func newConfigRefreshCmd() *cobra.Command {
	var dryRun bool
	var diffOnly bool
	var authEndpointOverride string

	cmd := &cobra.Command{
		Use:          "refresh",
		Short:        "Re-pull the active slot's config from the cluster auth service",
		SilenceUsage: true,
		Long: `Fetch the latest rendered config for the active slot from the cluster's
auth service and replace the active context in ~/.abc/config.yaml with it.

Use this after an admin rotates your credentials, on a fresh laptop, or to
recover from a local-edit mishap. Other contexts in your config (if any)
are preserved — only the active context is replaced.

Auth: the active context's access_token (Bearer) is sent to
GET /slots/me/config on the cluster auth service. The auth-svc URL is
derived from the active context's cluster endpoint (e.g.
` + "`https://nomad.seedling.abc-cluster.cloud`" + ` → ` + "`https://auth.seedling.abc-cluster.cloud`" + `).
Override with --auth-endpoint if the convention does not match.

Examples:
  abc auth config refresh
  abc auth config refresh --dry-run     # show what would change, don't write
  abc auth config refresh --diff        # print the server-returned YAML and exit
  abc auth config refresh --auth-endpoint https://auth.example.com
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return fmt.Errorf("load local config: %w", err)
			}
			activeName := strings.TrimSpace(cfg.ActiveContext)
			if activeName == "" {
				return fmt.Errorf("no active context — run `abc auth context use <name>` first")
			}
			actx := cfg.ActiveCtx()

			token := strings.TrimSpace(actx.AccessToken)
			if token == "" && actx.Admin.Services.Nomad != nil {
				token = strings.TrimSpace(actx.Admin.Services.Nomad.Token)
			}
			if token == "" {
				return fmt.Errorf("no access token in active context %q; cannot authenticate to auth-svc", activeName)
			}

			authEndpoint := strings.TrimSpace(authEndpointOverride)
			if authEndpoint == "" {
				authEndpoint, err = deriveAuthEndpoint(actx.Endpoint)
				if err != nil {
					return fmt.Errorf("derive auth-svc endpoint from %q: %w (use --auth-endpoint to override)",
						actx.Endpoint, err)
				}
			}
			endpoint := strings.TrimRight(authEndpoint, "/") + "/slots/me/config"

			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "text/yaml, application/yaml")

			client := debuglog.NewLoggingClient(&http.Client{Timeout: 15 * time.Second})
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("GET %s: %w", endpoint, err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read response from %s: %w", endpoint, err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s returned %d: %s",
					endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
			}

			// Parse the blob through the same normalisation pipeline as a
			// fresh on-disk load so the merged result is consistent.
			serverCfg, err := abccfg.ParseBytes(body)
			if err != nil {
				return fmt.Errorf("parse server-returned YAML: %w", err)
			}
			srvCtxName := strings.TrimSpace(serverCfg.ActiveContext)
			if srvCtxName == "" {
				// Fall back: the only key under contexts:
				for name := range serverCfg.Contexts {
					srvCtxName = name
					break
				}
			}
			if srvCtxName == "" {
				return fmt.Errorf("server returned an empty config (no contexts)")
			}
			srvCtx, ok := serverCfg.Contexts[srvCtxName]
			if !ok {
				return fmt.Errorf("server's active_context %q is not in the returned contexts map", srvCtxName)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Refresh source: %s\n", endpoint)
			fmt.Fprintf(out, "Server context: %q  (%d bytes returned)\n", srvCtxName, len(body))
			if _, exists := cfg.Contexts[srvCtxName]; exists {
				fmt.Fprintf(out, "Local action  : replace existing context %q\n", srvCtxName)
			} else {
				fmt.Fprintf(out, "Local action  : add new context %q\n", srvCtxName)
			}
			if cfg.ActiveContext != srvCtxName {
				fmt.Fprintf(out, "Local action  : update active_context (%q → %q)\n",
					cfg.ActiveContext, srvCtxName)
			}

			if diffOnly {
				fmt.Fprintln(out, "\n--- server-returned YAML ---")
				_, _ = out.Write(body)
				return nil
			}
			if dryRun {
				fmt.Fprintln(out, "\n[--dry-run] not writing ~/.abc/config.yaml")
				return nil
			}

			if cfg.Contexts == nil {
				cfg.Contexts = map[string]abccfg.Context{}
			}
			cfg.Contexts[srvCtxName] = srvCtx
			cfg.ActiveContext = srvCtxName

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("save local config: %w", err)
			}
			fmt.Fprintf(out, "\n✓ refreshed context %q from %s\n", srvCtxName, endpoint)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Fetch and report what would change, don't write")
	cmd.Flags().BoolVar(&diffOnly, "diff", false, "Print the server-returned YAML and exit (for external diff)")
	cmd.Flags().StringVar(&authEndpointOverride, "auth-endpoint", "",
		"Override the auth-svc URL (default: derive from the active context's cluster endpoint)")
	return cmd
}

// deriveAuthEndpoint converts a cluster endpoint URL into its auth-svc sibling
// by swapping the first DNS label. The cluster naming convention is
// `<service>.<tier>.abc-cluster.cloud`, so:
//
//	https://nomad.seedling.abc-cluster.cloud  →  https://auth.seedling.abc-cluster.cloud
//
// If the endpoint does not match the convention, callers should use the
// --auth-endpoint flag instead.
func deriveAuthEndpoint(clusterEndpoint string) (string, error) {
	clusterEndpoint = strings.TrimSpace(clusterEndpoint)
	if clusterEndpoint == "" {
		return "", fmt.Errorf("active context has no endpoint")
	}
	u, err := url.Parse(clusterEndpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute URL: %q", clusterEndpoint)
	}
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("host %q does not have a <subdomain>.<rest> shape", u.Host)
	}
	return fmt.Sprintf("%s://auth.%s", u.Scheme, parts[1]), nil
}
