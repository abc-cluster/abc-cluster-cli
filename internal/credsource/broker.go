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

	retry "github.com/avast/retry-go/v4"
)

// Broker retry tunables. Tight defaults — the broker is a user-facing dep
// on the hot path of every CLI command for a seedling/v1 slot, so we'd
// rather fail loud than mask a real outage with long backoff. The total
// worst-case wall-time is roughly:
//
//     BrokerRetryMaxAttempts × (BrokerRetryMaxBackoff + per-call timeout)
//     = 3 × (2s + 15s) ~= 51s upper bound, typical < 5s
//
// Override with the constants below for tests.
var (
	BrokerRetryMaxAttempts uint          = 3
	BrokerRetryMinBackoff  time.Duration = 200 * time.Millisecond
	BrokerRetryMaxBackoff  time.Duration = 2 * time.Second
)

// brokerRetryableStatusError signals fetch() that a status code is worth
// retrying. Non-retryable HTTP failures (e.g. 401, 403, 404, 409) are
// returned as plain errors which retry-go won't re-invoke. We use a sentinel
// error type so retry.RetryIf can distinguish.
type brokerRetryableStatusError struct {
	status int
	body   string
	url    string
}

func (e *brokerRetryableStatusError) Error() string {
	return fmt.Sprintf("broker %s returned retryable status %d: %s", e.url, e.status, strings.TrimSpace(e.body))
}

// isRetryableHTTPStatus reports whether an HTTP status code from the broker
// should trigger a retry. We retry transient server failures (500/502/503/504)
// + 429 (Too Many Requests). 4xx other than 429 are deliberate refusals from
// the broker (auth/permission/shape) and must surface immediately.
func isRetryableHTTPStatus(code int) bool {
	switch code {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		http.StatusTooManyRequests:
		return true
	}
	return false
}

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
// Retries transient failures (network + 5xx + 429) with exponential backoff
// via avast/retry-go; never retries 4xx (a 401 from a revoked opaque must
// surface immediately so admin revocations are visible to the user).
func (b *BrokerCredSource) fetch(ctx context.Context) (*Creds, error) {
	var body []byte

	// once is one attempt of the HTTP exchange. It returns:
	//   - nil on success (body is populated for the caller below)
	//   - *brokerRetryableStatusError on transient HTTP failure (retry-go will retry)
	//   - any other error on non-retryable HTTP failure or transport error
	// Transport errors from HTTPClient.Do (DNS, TCP RST, TLS handshake) are
	// returned as-is — retry-go retries them by default since they have no
	// retry.Unrecoverable wrapper.
	once := func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.ExchangeURL, nil)
		if err != nil {
			// Bad request-construction is not a transient condition.
			return retry.Unrecoverable(fmt.Errorf("%s: build request: %w", b.source, err))
		}
		req.Header.Set("Authorization", "Bearer "+b.OpaqueToken)
		req.Header.Set("Accept", "application/json")

		resp, err := b.HTTPClient.Do(req)
		if err != nil {
			// Network-level failure (DNS, dial, TLS, read). Retryable by default.
			return fmt.Errorf("%s: POST %s: %w", b.source, b.ExchangeURL, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, brokerErrorBodyLimit+1<<20)) // 1MB cap on success body
		if err != nil {
			// Mid-response failure — usually transient (connection reset).
			return fmt.Errorf("%s: read response: %w", b.source, err)
		}

		if resp.StatusCode == http.StatusOK {
			body = raw
			return nil
		}

		preview := string(raw)
		if len(preview) > brokerErrorBodyLimit {
			preview = preview[:brokerErrorBodyLimit] + "…(truncated)"
		}
		if isRetryableHTTPStatus(resp.StatusCode) {
			return &brokerRetryableStatusError{
				status: resp.StatusCode,
				body:   preview,
				url:    b.ExchangeURL,
			}
		}
		// 4xx (except 429): the broker is deliberately refusing. Don't retry.
		return retry.Unrecoverable(fmt.Errorf("%s: broker %s returned %d: %s",
			b.source, b.ExchangeURL, resp.StatusCode, strings.TrimSpace(preview)))
	}

	err := retry.Do(once,
		retry.Context(ctx),
		retry.Attempts(BrokerRetryMaxAttempts),
		retry.Delay(BrokerRetryMinBackoff),
		retry.MaxDelay(BrokerRetryMaxBackoff),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return nil, err
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
