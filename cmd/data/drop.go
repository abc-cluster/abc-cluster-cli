package data

// drop.go — `abc data drop`
//
// Limited-time, self-destructing OUTBOUND transfer. Uploads a local file to the
// seedling self-hosted transfer.sh endpoint (behind Caddy forward_auth) and
// prints an expiring share URL. The payload auto-deletes at the earlier of
// --expires or --max-downloads (transfer.sh), with a MinIO lifecycle rule as the
// authoritative backstop.
//
// EXPERIMENT (roadmap Phase 6.5). Distinct from:
//   - `abc data presign` — expiring URL to a PERSISTENT object (link dies, object stays)
//   - `abc data share`   — intra-group server-side copy
//   - `abc data push/pull/stage/transfer` — internal data movement
//
// Auth: the request carries the caller's Nomad token as `Authorization: Bearer`
// (same as `abc data upload`); Caddy's forward_auth /verify validates it.
//
// transfer.sh protocol: PUT <endpoint>/<filename>, with optional request headers
// `Max-Downloads` and `Max-Days`; the response body is the share URL and the
// `x-url-delete` response header is the one-shot delete URL.

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// maxDropExpiry caps --expires. The bucket lifecycle expires objects at 8
// days and transfer.sh purges at 7; anything longer is meaningless. 7d also
// matches `abc data presign`, keeping the duration vocabulary consistent.
const maxDropExpiry = 7 * 24 * time.Hour

// dropTransferLabel is the DNS label for the transfer service subdomain.
const dropTransferLabel = "transfer"

// dropServiceLabels mirrors cmd/portal's knownServiceLabels: leading
// per-service DNS labels stripped from the context endpoint before the
// transfer subdomain is prepended (so we never build transfer.nomad.<base>).
var dropServiceLabels = []string{"nomad", "s3", "minio", "workbench", "upload", "grafana", "api", "transfer"}

func newDropCmd() *cobra.Command {
	var (
		expires      string
		maxDownloads int
		endpoint     string
		token        string
		progress     bool
	)

	cmd := &cobra.Command{
		Use:          "drop <file>",
		Aliases:      []string{"send"}, // shipped as `send` in v0.1.40; kept as an alias
		Short:        "Drop a file as a limited-time, self-destructing download link",
		SilenceUsage: true,
		Long: `Upload a local file and print an expiring, self-destructing share URL.
The payload auto-deletes at the earlier of --expires or --max-downloads.

Unlike 'abc data presign' (which makes an expiring link to an object that STAYS
in a bucket), drop UPLOADS the file and the payload itself is deleted when the
link expires or its download budget is spent.

SENSITIVITY: drop links are for NON-SENSITIVE artifacts only — figures,
reports, small derived/aggregate results. Never PHI or raw/identifiable genomic
data: the link leaves the in-region storage boundary when a recipient fetches it.

Examples:

  # 7-day link (default):
  abc data drop ./results-summary.csv

  # One-shot, 24-hour link:
  abc data drop ./figure.png --max-downloads 1 --expires 24h

  # Pipe-friendly (the URL is the only thing on stdout):
  URL="$(abc data drop ./report.pdf)"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dur, err := time.ParseDuration(expires)
			if err != nil {
				return fmt.Errorf("--expires %q: %w (use Go duration: 30m, 4h, 168h)", expires, err)
			}
			if dur <= 0 {
				return fmt.Errorf("--expires must be positive")
			}
			if dur > maxDropExpiry {
				return fmt.Errorf("--expires cannot exceed 7 days (the drop bucket auto-expires objects at 8d)")
			}
			if maxDownloads < 0 {
				return fmt.Errorf("--max-downloads cannot be negative")
			}

			filePath := args[0]
			info, err := os.Stat(filePath)
			if err != nil {
				return fmt.Errorf("cannot read %q: %w", filePath, err)
			}
			if info.IsDir() {
				return fmt.Errorf("%q is a directory; drop takes a single file (tar it first)", filePath)
			}

			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()

			ep, err := resolveDropEndpoint(cmd, endpoint, actx)
			if err != nil {
				return err
			}

			bearer := resolveUploadToken(cmd, token, "")
			if strings.TrimSpace(bearer) == "" {
				return fmt.Errorf("no auth token available — set a context with a token, or pass --token")
			}

			maxDays := dropMaxDays(dur)

			res, err := dropUpload(cmd.Context(), ep, bearer, filePath, info.Size(), maxDays, maxDownloads, cmd.ErrOrStderr(), progress)
			if err != nil {
				return err
			}

			outW := cmd.OutOrStdout()
			errW := cmd.ErrOrStderr()

			downloads := "unlimited (within the expiry window)"
			if maxDownloads > 0 {
				downloads = fmt.Sprintf("%d", maxDownloads)
			}

			// The share link is the headline. Interactive: a bold label + the URL
			// on stdout. Piped/captured (stdout not a TTY): bare URL only, so
			// `URL="$(abc data drop …)"` still gets a clean URL.
			if supportsInteractiveProgress(outW) {
				fmt.Fprintf(outW, "\n  %s\n  %s\n", styleBold("Share link:", true), res.url)
			} else {
				fmt.Fprintln(outW, res.url)
			}

			// Metadata + the (important) sovereignty warning on stderr.
			styleOn := supportsInteractiveProgress(errW)
			fmt.Fprintf(errW, "  expires in %d day(s)  ·  downloads: %s\n", maxDays, downloads)
			fmt.Fprintf(errW, "\n  ⚠ NON-SENSITIVE artifacts only — the link leaves the in-region\n")
			fmt.Fprintf(errW, "    storage boundary when fetched. Never PHI or genomic data.\n")

			// Delete link is secondary: dimmed + last so it never competes with
			// the share link above.
			if res.deleteURL != "" {
				fmt.Fprintf(errW, "\n%s\n", styleDim("  revoke this link early: "+res.deleteURL, styleOn))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&expires, "expires", "168h", "expiry duration (e.g. 30m, 24h, 168h; max 7d)")
	cmd.Flags().IntVar(&maxDownloads, "max-downloads", 0, "delete after N downloads (0 = unlimited within the expiry window)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "transfer endpoint URL (or set ABC_TRANSFER_ENDPOINT; default derived from the active context)")
	cmd.Flags().StringVar(&token, "token", "", "bearer token override (default: the active context's token)")
	cmd.Flags().BoolVar(&progress, "progress", true, "show a live upload progress bar (rendered on stderr)")
	return cmd
}

// styleBold / styleDim wrap text in ANSI SGR codes when styling is on (the
// target stream is a TTY). No-ops otherwise, so piped/captured output stays
// clean. Kept dependency-free (raw ANSI) rather than pulling in lipgloss.
func styleBold(s string, on bool) string {
	if !on {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

func styleDim(s string, on bool) string {
	if !on {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// progressReadCloser wraps a file reader, reporting bytes read to a
// progressReporter as the HTTP client streams the request body.
type progressReadCloser struct {
	r  io.Reader
	pr *progressReporter
}

func (p *progressReadCloser) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.pr != nil {
		p.pr.Add(int64(n))
	}
	return n, err
}

// dropMaxDays converts an expiry duration to transfer.sh's integer
// `Max-Days` header: ceil to whole days, floored at 1.
func dropMaxDays(d time.Duration) int {
	days := int(math.Ceil(d.Hours() / 24))
	if days < 1 {
		days = 1
	}
	return days
}

type dropResult struct {
	url       string
	deleteURL string
}

// dropUpload PUTs the file to <endpoint>/<basename> and returns the share URL.
func dropUpload(ctx context.Context, endpoint, bearer, filePath string, size int64, maxDays, maxDownloads int, out io.Writer, progressEnabled bool) (dropResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	f, err := os.Open(filePath)
	if err != nil {
		return dropResult{}, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer f.Close()

	name := filepath.Base(filePath)
	putURL := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(name)

	// Live progress bar (stderr) as the request body streams. Disabled
	// automatically when out is not a TTY (e.g. piped) so stdout stays clean.
	pr := newProgressReporter(out, progressEnabled, "Droping "+name, size)
	var body io.Reader = f
	if pr.enabled {
		body = &progressReadCloser{r: f, pr: pr}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, body)
	if err != nil {
		pr.Complete()
		return dropResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.ContentLength = size // let transfer.sh enforce its max-upload-size accurately
	req.Header.Set("Max-Days", strconv.Itoa(maxDays))
	if maxDownloads > 0 {
		req.Header.Set("Max-Downloads", strconv.Itoa(maxDownloads))
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	pr.Complete() // tear down the bar before returning or printing anything else
	if err != nil {
		return dropResult{}, fmt.Errorf("upload to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return dropResult{}, fmt.Errorf("upload rejected (%s) — token not accepted by forward_auth at %s", resp.Status, endpoint)
	}
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		return dropResult{}, fmt.Errorf("file too large for the transfer service — for larger objects, put them in a bucket and use `abc data presign` instead")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dropResult{}, fmt.Errorf("upload failed (%s): %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	shareURL := strings.TrimSpace(string(respBody))
	if shareURL == "" || !strings.HasPrefix(shareURL, "http") {
		return dropResult{}, fmt.Errorf("upload succeeded but no share URL returned (body: %q)", string(respBody))
	}
	return dropResult{url: shareURL, deleteURL: strings.TrimSpace(resp.Header.Get("X-Url-Delete"))}, nil
}

// resolveDropEndpoint picks the transfer endpoint in priority order:
//  1. --endpoint flag
//  2. ABC_TRANSFER_ENDPOINT
//  3. derived from the active context (AuthEndpoint, then Endpoint): strip a
//     leading service label and prepend "transfer." → https://transfer.<base>.
func resolveDropEndpoint(cmd *cobra.Command, flagEndpoint string, actx abccfg.Context) (string, error) {
	if v := strings.TrimSpace(flagEndpoint); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	if v := strings.TrimSpace(envvars.Get("ABC_TRANSFER_ENDPOINT")); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	for _, base := range []string{actx.AuthEndpoint, actx.Endpoint} {
		if d := deriveTransferFromBase(base); d != "" {
			return d, nil
		}
	}
	return "", fmt.Errorf("could not derive a transfer endpoint from the active context; pass --endpoint or set ABC_TRANSFER_ENDPOINT")
}

// deriveTransferFromBase turns a context URL like
// https://workbench.seedling.abc-cluster.cloud (or .../api, .../nomad) into
// https://transfer.seedling.abc-cluster.cloud. Returns "" if base is empty or
// has a bare IP/port host (no DNS labels to recompose).
func deriveTransferFromBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Host
	// Skip host:port / bare IPs — no subdomain to recompose.
	if strings.Contains(host, ":") {
		return ""
	}
	if !strings.Contains(host, ".") {
		return ""
	}
	// If the first label is already "transfer", reuse as-is.
	for _, svc := range dropServiceLabels {
		if strings.HasPrefix(host, svc+".") {
			host = host[len(svc)+1:]
			break
		}
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s", scheme, dropTransferLabel, host)
}
