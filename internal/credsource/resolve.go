package credsource

import (
	"context"
	"sync"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ResolveFromContext is the single-shot helper most call sites should use.
// It selects the resolver for the given context, calls Resolve, and returns
// the result. On `cred_source: ""` / `"local"` this is a zero-cost field read
// (LocalCredSource); on broker tiers it makes one HTTP exchange (or hits the
// in-memory cache).
//
// The returned *Creds is shared across callers via the process-level resolver
// cache below — so two callers in one `abc` invocation share one broker hit.
func ResolveFromContext(ctx context.Context, abcCtx abccfg.Context) (*Creds, error) {
	cs, err := selectCached(abcCtx)
	if err != nil {
		return nil, err
	}
	return cs.Resolve(ctx)
}

// ----------------------------------------------------------------------------
// Process-level resolver cache
//
// Within a single `abc` invocation, many helper functions independently call
// `Select(ctx).Resolve(...)`. Without this cache, each call would build a new
// resolver instance — and broker resolvers would each have their own empty
// in-memory cache, defeating the 5-minute TTL. Sharing one resolver instance
// across the process lets the broker TTL actually amortise across callers.
//
// The cache key is the context name + the access_token (the opaque). Two
// contexts with identical access_tokens but different names should produce
// different resolvers (defensive — they can't really collide in practice,
// but we don't want to pretend two named contexts are the same).
//
// Cache key intentionally omits other fields: re-loading the config to pick
// up an endpoint change is unusual within a single invocation, and the
// LocalCredSource doesn't cache anyway.
// ----------------------------------------------------------------------------

var (
	resolverCacheMu sync.Mutex
	resolverCache   = map[string]CredSource{}
)

func resolverCacheKey(abcCtx abccfg.Context) string {
	// Use cred_source + access_token. cred_source disambiguates two
	// adjacent contexts pointing at the same broker URL; access_token
	// disambiguates two seedling/v1 contexts under different slots.
	return abcCtx.CredSource + "\x00" + abcCtx.AccessToken
}

func selectCached(abcCtx abccfg.Context) (CredSource, error) {
	key := resolverCacheKey(abcCtx)
	resolverCacheMu.Lock()
	if cs, ok := resolverCache[key]; ok {
		resolverCacheMu.Unlock()
		return cs, nil
	}
	resolverCacheMu.Unlock()

	cs, err := Select(abcCtx)
	if err != nil {
		return nil, err
	}

	resolverCacheMu.Lock()
	// Two goroutines racing: keep whichever already-cached one if a peer beat us.
	if existing, ok := resolverCache[key]; ok {
		resolverCacheMu.Unlock()
		return existing, nil
	}
	resolverCache[key] = cs
	resolverCacheMu.Unlock()
	return cs, nil
}

// ResetResolverCache clears the process-level resolver cache. Tests use this
// to ensure each test starts from a clean state.
func ResetResolverCache() {
	resolverCacheMu.Lock()
	resolverCache = map[string]CredSource{}
	resolverCacheMu.Unlock()
}

// IsBroker reports whether the given cred_source value routes through a
// broker (and therefore Resolve may block on the network on cache miss).
// Callers that want to avoid surprise network IO can guard with this.
func IsBroker(credSource string) bool {
	switch normalize(credSource) {
	case "seedling/v1", "grove/v1", "cloud/v1":
		return true
	default:
		return false
	}
}
