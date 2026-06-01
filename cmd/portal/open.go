package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newOpenCmd() *cobra.Command {
	var linkOnly bool

	cmd := &cobra.Command{
		Use:   "open <portal>",
		Short: "Open a portal in the browser, pre-authenticated",
		Long: `Open a cluster portal in the default browser using credentials from the
active context. No manual token copy-paste required.

Portals and their auth methods:

  nomad      Token injected as ?token= URL param (Nomad native)
  grafana    Magic link: abc-auth-svc issues a one-time code → sets session
  workbench  Magic link (same as grafana)
  upload     Magic link (same as grafana)
  minio      MinIO SSO via abc-auth-svc; logs into console directly

Use --link to print the pre-authenticated URL instead of opening the browser.
Useful for SSH sessions, sharing, or scripting.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"nomad", "grafana", "workbench", "upload", "minio"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.ToLower(args[0])
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			ctx := cfg.ActiveCtx()
			urls, err := DeriveURLs(ctx)
			if err != nil {
				return err
			}
			return openPortal(name, ctx, urls, linkOnly)
		},
	}

	cmd.Flags().BoolVar(&linkOnly, "link", false, "Print the pre-authenticated URL instead of opening the browser")
	return cmd
}

// openPortal dispatches to the correct auth mechanism per portal.
func openPortal(name string, ctx config.Context, urls PortalURLs, linkOnly bool) error {
	switch name {
	case "nomad":
		return openNomad(ctx, urls, linkOnly)
	case "grafana":
		return openMagicLink(urls.Grafana, urls, ctx.NomadToken(), linkOnly)
	case "workbench":
		return openMagicLink(urls.Workbench, urls, ctx.NomadToken(), linkOnly)
	case "upload":
		return openMagicLink(urls.Upload, urls, ctx.NomadToken(), linkOnly)
	case "minio":
		return openMinIOSSO(ctx, urls, linkOnly)
	default:
		return fmt.Errorf("unknown portal %q — valid: nomad, grafana, workbench, upload, minio", name)
	}
}

// ── nomad: token-in-URL ───────────────────────────────────────────────────────

func openNomad(ctx config.Context, urls PortalURLs, linkOnly bool) error {
	tok := ctx.NomadToken()
	if tok == "" {
		return fmt.Errorf("no Nomad token in active context — run 'abc auth login' first")
	}
	u, err := url.Parse(urls.Nomad)
	if err != nil {
		return fmt.Errorf("invalid Nomad URL %q: %w", urls.Nomad, err)
	}
	q := u.Query()
	q.Set("token", tok)
	u.RawQuery = q.Encode()
	finalURL := u.String()

	if !linkOnly {
		fmt.Fprintf(os.Stderr, "[abc] opening Nomad UI (token injected)\n")
		fmt.Fprintf(os.Stderr, "  %s\n", urls.Nomad)
	}
	return openBrowser(finalURL, linkOnly)
}

// ── magic link: workbench / grafana / upload ──────────────────────────────────
//
// Flow:
//   1. CLI POST /auth/cli-token  {nomad_token, next}  → {url: "https://.../auth/redeem?code=..."}
//   2. CLI opens returned URL in browser
//   3. Browser visits /auth/redeem → abc-auth-svc sets session cookie (Domain=.seedling.*) → redirect to next

type cliTokenRequest struct {
	NomadToken string `json:"nomad_token"`
	Next       string `json:"next"`
}

type cliTokenResponse struct {
	Code string `json:"code"`
	TTL  int    `json:"ttl"`
}

func openMagicLink(targetURL string, urls PortalURLs, nomadToken string, linkOnly bool) error {
	return openMagicLinkPortal(targetURL, "workbench", urls, nomadToken, linkOnly)
}

func openMagicLinkPortal(targetURL string, portal string, urls PortalURLs, nomadToken string, linkOnly bool) error {
	if nomadToken == "" {
		return fmt.Errorf("no Nomad token in active context — run 'abc auth login' first")
	}

	authBase := urls.AuthSvcBase() // e.g. https://workbench.seedling.abc-cluster.cloud
	endpoint := authBase + "/auth/cli-token"

	reqBody, _ := json.Marshal(cliTokenRequest{
		NomadToken: nomadToken,
		Next:       targetURL,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("could not reach auth service at %s: %w\n"+
			"  Is the cluster reachable? Try: abc doctor", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("token rejected by auth service — token may be expired\n" +
			"  Run 'abc context list' to check your active context")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth service returned %d: %s", resp.StatusCode, string(body))
	}

	var tok cliTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil || tok.Code == "" {
		return fmt.Errorf("unexpected auth service response: %s", string(body))
	}

	// Construct the redeem URL from the known authBase — do NOT use a URL
	// returned by the server which would reflect the internal Tailscale host.
	var redeemURL string
	if portal == "minio" {
		// MinIO SSO uses a dedicated endpoint served from the minio subdomain
		// so the session cookie is set for minio.seedling.abc-cluster.cloud
		redeemURL = urls.MinIO + "/auth/minio-login?code=" + tok.Code
	} else {
		redeemURL = authBase + "/auth/redeem?code=" + tok.Code
	}

	portalName := portalLabelFromURL(targetURL)
	if !linkOnly {
		fmt.Fprintf(os.Stderr, "[abc] opening %s (magic link, 60s TTL)\n", portalName)
		fmt.Fprintf(os.Stderr, "  %s\n", targetURL)
	}
	return openBrowser(redeemURL, linkOnly)
}

func portalLabelFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	parts := strings.SplitN(host, ".", 2)
	if len(parts) > 0 {
		return strings.Title(parts[0])
	}
	return host
}

// ── minio: SSO via abc-auth-svc MinIO login proxy ────────────────────────────
//
// abc-auth-svc's /auth/cli-token validates the Nomad token, calls MinIO's
// /api/v1/login internally, and stores the resulting MinIO JWT in a one-time
// code. The browser visits minio.seedling.*/auth/minio-login?code=<code> which
// is routed to abc-auth-svc; it redeems the code and sets the MinIO token cookie
// for the minio.* domain, then redirects to the MinIO console root.

func openMinIOSSO(ctx config.Context, urls PortalURLs, linkOnly bool) error {
	tok := ctx.NomadToken()
	if tok == "" {
		return fmt.Errorf("no Nomad token in active context — run 'abc auth login' first")
	}

	minioUser := minioAccessKey(ctx)
	if !linkOnly {
		fmt.Fprintf(os.Stderr, "[abc] opening MinIO console for %s (SSO via abc-auth-svc)\n", minioUser)
		fmt.Fprintf(os.Stderr, "  %s\n", urls.MinIO)
	}
	return openMagicLinkPortal(urls.MinIO, "minio", urls, tok, linkOnly)
}

// minioAccessKey returns the MinIO access key (username) from the active context.
// For pool users: stored in admin.services.minio.cred_source.local["user"].
// Falls back to the Nomad token name if not explicitly configured.
func minioAccessKey(ctx config.Context) string {
	if ctx.Admin.Services.MinIO != nil {
		svc := ctx.Admin.Services.MinIO
		// Prefer cred_source.local["user"]
		if svc.CredSource != nil && len(svc.CredSource.Local) > 0 {
			if u := svc.CredSource.Local["user"]; u != "" {
				return u
			}
		}
		// Fallback: top-level User field
		if svc.User != "" {
			return svc.User
		}
	}
	return "(unknown — check admin.services.minio in config.yaml)"
}

// ── browser open ─────────────────────────────────────────────────────────────

// openBrowser opens rawURL in the OS default browser, or prints it when
// linkOnly is true or when no display is available (SSH session).
func openBrowser(rawURL string, linkOnly bool) error {
	if linkOnly {
		fmt.Println(rawURL)
		return nil
	}

	// Detect headless Linux (SSH without display).
	if runtime.GOOS == "linux" {
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			fmt.Println(rawURL)
			fmt.Fprintln(os.Stderr, "[abc] no display detected (SSH?); URL printed above — open it manually")
			return nil
		}
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux", "freebsd", "openbsd", "netbsd":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		fmt.Println(rawURL)
		return nil
	}
	if err := cmd.Start(); err != nil {
		fmt.Println(rawURL)
		fmt.Fprintf(os.Stderr, "[abc] could not launch browser (%v); URL printed above\n", err)
	}
	return nil
}
