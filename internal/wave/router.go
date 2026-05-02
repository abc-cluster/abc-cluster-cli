// Package wave provides hybrid Wave endpoint routing for abc-cluster-cli.
//
// Routing model:
//   - Build operations (wave-exec / conda→container): always Seqera Wave.
//     Wave Lite (abc-wave) does not implement the build service.
//   - Augment operations (Nextflow pipeline wave.endpoint): probe abc-wave first;
//     fall back to Seqera Wave if abc-wave is absent or unhealthy.
//
// The probe result is cached for 5 minutes to avoid blocking every pipeline
// submission. Set AbcWaveURL = "" to force Seqera Wave unconditionally.
package wave

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SeqeraWaveURL is the public Seqera Wave service endpoint.
	SeqeraWaveURL = "https://wave.seqera.io"

	// probePath is the lightweight health endpoint on Wave Lite.
	probePath = "/v1alpha2/info"

	// probeTTL is how long a successful probe result is cached.
	probeTTL = 5 * time.Minute

	// probeTimeout is the per-request deadline for the abc-wave health check.
	probeTimeout = 3 * time.Second
)

// Router selects the Wave endpoint for a given operation type.
type Router struct {
	// AbcWaveURL is the URL of the locally deployed abc-wave Lite service
	// (e.g. "http://100.126.253.95:9090"). When empty, all operations are
	// routed to Seqera Wave.
	AbcWaveURL string

	mu          sync.Mutex
	cachedOK    bool
	cachedAt    time.Time
	probeClient *http.Client
}

// NewRouter constructs a Router. abcWaveURL is read from
// admin.services.wave.http; pass "" to disable abc-wave routing.
func NewRouter(abcWaveURL string) *Router {
	return &Router{
		AbcWaveURL: strings.TrimRight(abcWaveURL, "/"),
		probeClient: &http.Client{Timeout: probeTimeout},
	}
}

// EndpointForBuild always returns the Seqera Wave URL.
// Wave Lite does not implement the build service (conda→container),
// so wave-exec jobs must always target wave.seqera.io regardless of
// whether abc-wave is configured or reachable.
func (r *Router) EndpointForBuild() string {
	return SeqeraWaveURL
}

// EndpointForAugment returns the best available Wave endpoint for
// on-the-fly container augmentation (Nextflow wave.endpoint config).
//
// It probes abc-wave once per probeTTL window. If abc-wave is reachable,
// its URL is returned; otherwise Seqera Wave is used as fallback.
// The ctx is used only for the probe HTTP request; it is never stored.
func (r *Router) EndpointForAugment(ctx context.Context) string {
	if r.AbcWaveURL == "" {
		return SeqeraWaveURL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.cachedAt) < probeTTL {
		if r.cachedOK {
			return r.AbcWaveURL
		}
		return SeqeraWaveURL
	}
	ok := r.probe(ctx)
	r.cachedOK = ok
	r.cachedAt = time.Now()
	if ok {
		return r.AbcWaveURL
	}
	return SeqeraWaveURL
}

// probe performs a synchronous health check against the abc-wave /v1alpha2/info
// endpoint. Returns true if the service responds with HTTP 2xx.
// Must be called with r.mu held.
func (r *Router) probe(ctx context.Context) bool {
	url := fmt.Sprintf("%s%s", r.AbcWaveURL, probePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := r.probeClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// IsAbcWaveConfigured reports whether an abc-wave URL is set in this router.
func (r *Router) IsAbcWaveConfigured() bool {
	return r.AbcWaveURL != ""
}
