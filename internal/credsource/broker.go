package credsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// BrokerCredSource is shared infrastructure for resolvers that exchange an
// opaque user token for real credentials at a tier-specific broker URL.
//
// All `<tier>/v1` resolvers (seedling/v1, grove/v1, cloud/v1) embed this
// struct and share its caching + HTTP machinery; they differ only in the
// `Source` string they stamp into Creds and the tier-specific exchange URL.
//
// Cache discipline:
//   - Keyed by sha256-like fingerprint of the opaque (we never log the bare
//     token; the cache key is a short prefix safe for diagnostics).
//   - TTL is BrokerCacheTTL (default 5 minutes) — short enough that a
//     rotated upstream Nomad / MinIO secret resolves within minutes; long
//     enough to amortize the broker hop across a burst of CLI calls.
//   - Goroutine-safe (single mutex; small footprint, small contention).
//
// On a cache miss the broker is called synchronously; on a cache hit the
// cached Creds is returned without any network IO.
type BrokerCredSource struct {
	// Name is stamped into resolver.Name() and into the returned Creds.Source.
	source string

	// ExchangeURL is the absolute URL to POST the opaque to. Set by the
	// concrete resolver's constructor (computed from the context endpoint).
	ExchangeURL string

	// OpaqueToken is the bare opaque the CLI presents as Bearer. Never logged.
	OpaqueToken string

	// HTTPClient is overridable for tests.
	HTTPClient *http.Client

	// Whoami is propagated from the context (auth.whoami) without going
	// through the broker — purely for display + audit. The broker also
	// returns whoami in its response and we cross-check; mismatches are
	// surfaced as a clear error.
	ContextWhoami string

	mu       sync.Mutex
	cached   *Creds
	cachedAt time.Time
}

// BrokerCacheTTL is the in-memory cache window for a successful exchange.
// Five minutes balances broker-call amortisation with prompt visibility
// when an admin rotates upstream Nomad / MinIO credentials.
const BrokerCacheTTL = 5 * time.Minute

// brokerErrorBodyLimit caps how many bytes of a broker error body we
// preserve in the returned error — defends against pathological bodies.
const brokerErrorBodyLimit = 4096

// newBrokerCredSource is the shared constructor; tier-specific resolvers
// wrap it.
func newBrokerCredSource(source, exchangeURL, opaque, contextWhoami string) *BrokerCredSource {
	return &BrokerCredSource{
		source:        source,
		ExchangeURL:   exchangeURL,
		OpaqueToken:   opaque,
		ContextWhoami: contextWhoami,
		HTTPClient:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (b *BrokerCredSource) Name() string { return b.source }

// Resolve returns the broker's last-known answer if the cache is fresh; on
// cache miss it POSTs to ExchangeURL with the opaque as Bearer and stores
// the result. The cache is cleared on any non-200 response so a transient
// failure doesn't poison the next call.
func (b *BrokerCredSource) Resolve(ctx context.Context) (*Creds, error) {
	if b.OpaqueToken == "" {
		return nil, fmt.Errorf("%s: no opaque token in context (cred_source set but access_token empty)", b.source)
	}
	if b.ExchangeURL == "" {
		return nil, fmt.Errorf("%s: no broker exchange URL", b.source)
	}

	b.mu.Lock()
	if b.cached != nil && time.Since(b.cachedAt) < BrokerCacheTTL {
		c := b.cached
		b.mu.Unlock()
		return c, nil
	}
	b.mu.Unlock()

	creds, err := b.fetch(ctx)
	if err != nil {
		// Clear any stale cache on hard failure — don't paper over with the
		// last successful exchange when the broker is now actively rejecting.
		b.mu.Lock()
		b.cached = nil
		b.cachedAt = time.Time{}
		b.mu.Unlock()
		return nil, err
	}

	b.mu.Lock()
	b.cached = creds
	b.cachedAt = time.Now()
	b.mu.Unlock()
	return creds, nil
}

// InvalidateCache forces the next Resolve call to hit the broker. Useful
// for `abc auth refresh` style commands where the user knows the upstream
// changed.
func (b *BrokerCredSource) InvalidateCache() {
	b.mu.Lock()
	b.cached = nil
	b.cachedAt = time.Time{}
	b.mu.Unlock()
}

// fetch is the HTTP exchange itself, separated for unit-testability.
func (b *BrokerCredSource) fetch(ctx context.Context) (*Creds, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.ExchangeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", b.source, err)
	}
	req.Header.Set("Authorization", "Bearer "+b.OpaqueToken)
	req.Header.Set("Accept", "application/json")

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: POST %s: %w", b.source, b.ExchangeURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, brokerErrorBodyLimit+1<<20)) // 1MB cap on success body
	if err != nil {
		return nil, fmt.Errorf("%s: read response: %w", b.source, err)
	}

	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > brokerErrorBodyLimit {
			preview = preview[:brokerErrorBodyLimit] + "…(truncated)"
		}
		return nil, fmt.Errorf("%s: broker %s returned %d: %s",
			b.source, b.ExchangeURL, resp.StatusCode, strings.TrimSpace(preview))
	}

	var wire wireCreds
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%s: parse broker response: %w (first 200B: %q)",
			b.source, err, truncate(string(body), 200))
	}

	// Cross-check the source — defence against a misconfigured proxy
	// returning a different tier's bundle.
	if strings.TrimSpace(wire.Source) != "" && wire.Source != b.source {
		return nil, fmt.Errorf("%s: broker returned source=%q (expected %q) — possible misrouted exchange",
			b.source, wire.Source, b.source)
	}

	// Cross-check whoami if the context carried one. A mismatch means the
	// user's opaque resolved to a different slot than they think — surface
	// it loudly rather than silently dispatching jobs as someone else.
	if b.ContextWhoami != "" && strings.TrimSpace(wire.Whoami) != "" && wire.Whoami != b.ContextWhoami {
		return nil, fmt.Errorf("%s: broker whoami=%q but context auth.whoami=%q — refusing exchange",
			b.source, wire.Whoami, b.ContextWhoami)
	}

	out := &Creds{
		Whoami: firstNonEmpty(wire.Whoami, b.ContextWhoami),
		Source: b.source,
		Nomad: NomadCreds{
			Addr:        wire.Nomad.Addr,
			Token:       wire.Nomad.Token,
			Namespace:   wire.Nomad.Namespace,
			Datacenters: append([]string(nil), wire.Nomad.Datacenters...),
			HeadPool:    wire.Nomad.HeadPool,
			WorkerPool:  wire.Nomad.WorkerPool,
		},
		Minio: MinioCreds{
			Endpoint:  wire.Minio.Endpoint,
			AccessKey: wire.Minio.AccessKey,
			SecretKey: wire.Minio.SecretKey,
		},
	}

	if out.Nomad.Token == "" {
		return nil, errors.New(b.source + ": broker returned empty nomad.token")
	}
	return out, nil
}

// wireCreds matches the JSON shape returned by abc-auth-svc's _build_creds_bundle.
type wireCreds struct {
	Whoami string `json:"whoami"`
	Source string `json:"source"`
	Nomad  struct {
		Addr        string   `json:"addr"`
		Token       string   `json:"token"`
		Namespace   string   `json:"namespace"`
		Datacenters []string `json:"datacenters"`
		HeadPool    string   `json:"head_pool"`
		WorkerPool  string   `json:"worker_pool"`
	} `json:"nomad"`
	Minio struct {
		Endpoint  string `json:"endpoint"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	} `json:"minio"`
}

// deriveBrokerExchangeURL converts a cluster endpoint into the /auth/exchange
// URL by swapping the first DNS label.  Mirrors the deriveAuthEndpoint logic
// in cmd/auth/config.go (Phase 0); kept duplicated here to avoid a cmd/->cmd/
// import. Future cleanup: pull both call sites onto this helper.
//
//	https://nomad.seedling.abc-cluster.cloud   → https://auth.seedling.abc-cluster.cloud/auth/exchange
//	https://nomad.workbench.seedling.example   → https://auth.workbench.seedling.example/auth/exchange
//
// Returns an error rather than guessing if the endpoint isn't a recognisable
// absolute URL — the caller should surface this through an `--auth-endpoint`-
// style override.
func deriveBrokerExchangeURL(clusterEndpoint string) (string, error) {
	clusterEndpoint = strings.TrimSpace(clusterEndpoint)
	if clusterEndpoint == "" {
		return "", errors.New("empty cluster endpoint")
	}
	u, err := url.Parse(clusterEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", clusterEndpoint, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute URL: %q", clusterEndpoint)
	}
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("host %q does not have a <subdomain>.<rest> shape", u.Host)
	}
	return fmt.Sprintf("%s://auth.%s/auth/exchange", u.Scheme, parts[1]), nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
