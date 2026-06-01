package credsource

import (
	"fmt"
	"os"
	"strings"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// SeedlingV1CredSource resolves credentials against the seedling-tier broker
// (abc-auth-svc's POST /auth/exchange). The context's `access_token` is the
// bare opaque the user obtained at claim time (or via an admin cred-source
// flip). The exchange URL is derived from the context's cluster endpoint
// unless explicitly overridden by the caller (test seam).
//
// Server side: abc-auth-svc's _auth_exchange handler.
// Token shape: opaque starts with `abco_` (32 urlsafe bytes).
type SeedlingV1CredSource struct {
	*BrokerCredSource
}

// NewSeedlingV1 builds a resolver bound to the given context. The exchange
// URL is computed from ctx.Endpoint by swapping the first DNS label
// (`nomad.seedling.…` → `auth.seedling.…`).
func NewSeedlingV1(ctx abccfg.Context) (*SeedlingV1CredSource, error) {
	opaque := strings.TrimSpace(ctx.AccessToken)
	if opaque == "" {
		return nil, fmt.Errorf("seedling/v1: context has cred_source set but access_token is empty (no opaque to exchange)")
	}
	if !strings.HasPrefix(opaque, "abco_") {
		// Soft-warn shape — not a hard fail because the prefix may evolve,
		// but a user-visible diagnostic when something's clearly off.
		// We don't return — the broker will tell us authoritatively.
	}

	// Exchange URL resolution priority:
	//   1. ABC_AUTH_EXCHANGE_URL env var (operator escape hatch / tests)
	//   2. ctx.AuthEndpoint (server-stamped at claim time — preferred when set)
	//   3. derive from ctx.Endpoint by swapping the first DNS label to "auth"
	// The third leg is brittle by construction — deployments that publish
	// the broker under a different DNS prefix (e.g. `workbench.<rest>` for
	// seedling) MUST stamp ctx.AuthEndpoint via the renderer.
	exchURL := strings.TrimSpace(os.Getenv("ABC_AUTH_EXCHANGE_URL"))
	if exchURL == "" {
		exchURL = strings.TrimSpace(ctx.AuthEndpoint)
		if exchURL != "" {
			// AuthEndpoint may be a base URL or the full /auth/exchange URL.
			if !strings.HasSuffix(exchURL, "/auth/exchange") {
				exchURL = strings.TrimRight(exchURL, "/") + "/auth/exchange"
			}
		}
	}
	if exchURL == "" {
		derived, err := deriveBrokerExchangeURL(ctx.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("seedling/v1: derive exchange URL from endpoint %q: %w (set auth_endpoint on the context or ABC_AUTH_EXCHANGE_URL to override)", ctx.Endpoint, err)
		}
		exchURL = derived
	}

	whoami := ""
	if ctx.Auth != nil {
		whoami = strings.TrimSpace(ctx.Auth.Whoami)
	}

	return &SeedlingV1CredSource{
		BrokerCredSource: newBrokerCredSource("seedling/v1", exchURL, opaque, whoami),
	}, nil
}
