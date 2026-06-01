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
  minio      URL opened + MinIO credentials printed to terminal

If the terminal has no display (e.g. SSH session without DISPLAY/WAYLAND_DISPLAY),
the URL is printed instead of opened.`,
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
			return openPortal(name, ctx, urls)
		},
	}
	return cmd
}

// openPortal dispatches to the correct auth mechanism per portal.
func openPortal(name string, ctx config.Context, urls PortalURLs) error {
	switch name {
	case "nomad":
		return openNomad(ctx, urls)
	case "grafana":
		return openMagicLink(urls.Grafana, urls, ctx.NomadToken())
	case "workbench":
		return openMagicLink(urls.Workbench, urls, ctx.NomadToken())
	case "upload":
		return openMagicLink(urls.Upload, urls, ctx.NomadToken())
	case "minio":
		return openMinIO(ctx, urls)
	default:
		return fmt.Errorf("unknown portal %q — valid: nomad, grafana, workbench, upload, minio", name)
	}
}

// ── nomad: token-in-URL ───────────────────────────────────────────────────────

func openNomad(ctx config.Context, urls PortalURLs) error {
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

	fmt.Fprintf(os.Stderr, "[abc] opening Nomad UI (token injected)\n")
	fmt.Fprintf(os.Stderr, "  %s\n", urls.Nomad)
	return openBrowser(finalURL)
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
	URL string `json:"url"`
}

func openMagicLink(targetURL string, urls PortalURLs, nomadToken string) error {
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
	if err := json.Unmarshal(body, &tok); err != nil || tok.URL == "" {
		return fmt.Errorf("unexpected auth service response: %s", string(body))
	}

	// Parse the portal name from the target URL for display
	portalName := portalLabelFromURL(targetURL)
	fmt.Fprintf(os.Stderr, "[abc] opening %s (magic link, 60s TTL)\n", portalName)
	fmt.Fprintf(os.Stderr, "  %s\n", targetURL)
	return openBrowser(tok.URL)
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

// ── minio: open URL + print credentials ──────────────────────────────────────

func openMinIO(ctx config.Context, urls PortalURLs) error {
	s3 := ctx.Admin.Services.Nomad // reuse nomad block fields
	_ = s3

	// MinIO access key = Nomad token username (same credential)
	// For pool users this is their slot name (e.g. slot-bold_hornbill)
	accessKey := ctx.NomadToken() // SecretID used as both Nomad token AND MinIO password
	// The MinIO access key (username) comes from the context name or whoami
	// Best effort: show both and let the user choose
	fmt.Fprintf(os.Stderr, "[abc] opening MinIO console\n")
	fmt.Fprintf(os.Stderr, "  %s\n\n", urls.MinIO)
	fmt.Fprintf(os.Stderr, "MinIO credentials (from active context):\n")
	fmt.Fprintf(os.Stderr, "  Username: (see config.yaml — admin.services.nomad.token is your MinIO password)\n")
	fmt.Fprintf(os.Stderr, "  Password: %s\n", accessKey)
	fmt.Fprintf(os.Stderr, "\nThe password has been printed above — paste it into the MinIO login form.\n")
	return openBrowser(urls.MinIO)
}

// ── browser open ─────────────────────────────────────────────────────────────

// openBrowser opens url in the OS default browser.
// If no display is available (SSH without DISPLAY/WAYLAND_DISPLAY), it prints
// the URL to stdout instead so the user can open it manually.
func openBrowser(rawURL string) error {
	// Detect headless environment.
	if runtime.GOOS == "linux" {
		display := os.Getenv("DISPLAY")
		wayland := os.Getenv("WAYLAND_DISPLAY")
		if display == "" && wayland == "" {
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
		// Fallback: print the URL so the user can open manually
		fmt.Println(rawURL)
		fmt.Fprintf(os.Stderr, "[abc] could not launch browser (%v); URL printed above\n", err)
	}
	return nil
}
