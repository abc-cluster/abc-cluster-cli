package capability

import (
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// MockMap returns a builder that starts from a tier-default capability
// map and applies overrides. Used by verb-level tests so they don't
// touch a real cluster or the local SQLite.
//
// Pattern:
//
//	caps := capability.MockMap(capability.Tier("abc-grove")).
//	    Without("abc-accounting-svc").
//	    AtVersion("abc-policy-svc", "0.5.1").
//	    WithFeatures("abc-bitemporal-svc", "lucene-search").
//	    Build()
//
//	decision := capability.Require(reportCapabilities, caps, nil)
//	if decision.Backend != "local-state" {
//	    t.Errorf("expected local-state backend, got %q", decision.Backend)
//	}
func MockMap(tier ClusterType) *Builder {
	return &Builder{
		caps: tierDefault(tier),
	}
}

// ClusterType identifies which tier-default capability set to seed from.
// Aligns with the values stored in `~/.abc/config.yaml`'s
// `cluster_type` field.
type ClusterType string

// Tier wraps a string for clarity at call sites.
func Tier(s string) ClusterType { return ClusterType(s) }

const (
	TierSeedling   ClusterType = "abc-nodes"
	TierGrove      ClusterType = "abc-grove"
	TierGroveTended ClusterType = "abc-grove-tended"
	TierGarden     ClusterType = "abc-garden"
	TierCloud      ClusterType = "abc-cloud"
)

// Builder accumulates overrides and produces a *cfg.Capabilities.
type Builder struct {
	caps *cfg.Capabilities
}

// With overrides a single service entry. If the service didn't exist in
// the seeded map, it's added.
func (b *Builder) With(svc string, sc cfg.ServiceCapability) *Builder {
	if b.caps.Services == nil {
		b.caps.Services = map[string]cfg.ServiceCapability{}
	}
	b.caps.Services[svc] = sc
	return b
}

// Without marks a service as explicitly unavailable. For real services,
// this just deletes the entry. For local-state, it sets an explicit
// Available=false so the satisfies() check doesn't fall through to
// reading the actual on-disk SQLite (which would otherwise satisfy the
// requirement in a test environment with applied migrations).
func (b *Builder) Without(svc string) *Builder {
	if b.caps.Services == nil {
		b.caps.Services = map[string]cfg.ServiceCapability{}
	}
	if svc == "local-state" {
		b.caps.Services[svc] = cfg.ServiceCapability{Available: false}
	} else {
		delete(b.caps.Services, svc)
	}
	return b
}

// AtVersion sets a service's Version (and marks it Available=true if
// it wasn't already in the map).
func (b *Builder) AtVersion(svc, version string) *Builder {
	if b.caps.Services == nil {
		b.caps.Services = map[string]cfg.ServiceCapability{}
	}
	sc := b.caps.Services[svc]
	sc.Available = true
	sc.Version = version
	if cn := Codename(svc); cn != "" {
		sc.Codename = cn
	}
	b.caps.Services[svc] = sc
	return b
}

// WithFeatures appends features to a service's feature list (creating
// the service entry if absent).
func (b *Builder) WithFeatures(svc string, features ...string) *Builder {
	if b.caps.Services == nil {
		b.caps.Services = map[string]cfg.ServiceCapability{}
	}
	sc := b.caps.Services[svc]
	sc.Available = true
	if cn := Codename(svc); cn != "" {
		sc.Codename = cn
	}
	for _, f := range features {
		if !contains(sc.Features, f) {
			sc.Features = append(sc.Features, f)
		}
	}
	b.caps.Services[svc] = sc
	return b
}

// Stale sets LastSynced to a past time so Fresh() returns RevalidateInBg
// or BlockingProbe. Useful for testing the freshness layer in isolation.
func (b *Builder) Stale(age time.Duration) *Builder {
	b.caps.LastSynced = time.Now().Add(-age)
	return b
}

// Build finalises the builder. Sets LastSynced to now if not already set
// (so Fresh() returns FreshCache by default).
func (b *Builder) Build() *cfg.Capabilities {
	if b.caps.LastSynced.IsZero() {
		b.caps.LastSynced = time.Now()
	}
	return b.caps
}

// tierDefault returns the seeded capability map for a tier. Used as the
// starting point for tests and as the cold-start fallback per
// §"Discovery cascade — Nomad-job introspection map" of the brainstorm.
//
// Hand-curated rather than computed; updated when the tier-appearance
// table in design/exploring/service-naming-map.md changes.
func tierDefault(tier ClusterType) *cfg.Capabilities {
	caps := &cfg.Capabilities{
		SchemaVersion: 1,
		ProbeSource:   "tier-default",
		Services:      map[string]cfg.ServiceCapability{},
	}
	// local-state is always notionally available — verbs that only need
	// SQLite work at every tier.
	caps.Services["local-state"] = cfg.ServiceCapability{
		Available: true,
		Version:   "0001_initial", // floor; localStateSatisfies refines from the actual DB
	}

	switch tier {
	case TierSeedling:
		// No cluster services.
	case TierGrove:
		addService(caps, "abc-bitemporal-svc")
		addService(caps, "abc-policy-svc")
	case TierGroveTended:
		addService(caps, "abc-bitemporal-svc")
		addService(caps, "abc-policy-svc")
		addService(caps, "abc-controller-svc")
		addService(caps, "abc-accounting-svc")
		addService(caps, "abc-fleet-svc")
		addService(caps, "abc-telemetry-svc")
		addService(caps, "abc-chat-svc")
	case TierGarden, TierCloud:
		addService(caps, "abc-bitemporal-svc")
		addService(caps, "abc-policy-svc")
		addService(caps, "abc-controller-svc")
		addService(caps, "abc-accounting-svc")
		addService(caps, "abc-fleet-svc")
		addService(caps, "abc-telemetry-svc")
		addService(caps, "abc-chat-svc")
		addService(caps, "abc-client-web")
		if tier == TierCloud {
			addService(caps, "abc-marketplace-svc")
			addService(caps, "abc-billing-bridge")
			addService(caps, "abc-signup-svc")
		}
	}
	return caps
}

func addService(caps *cfg.Capabilities, techName string) {
	sc := cfg.ServiceCapability{
		Available: true,
	}
	if cn := Codename(techName); cn != "" {
		sc.Codename = cn
	}
	caps.Services[techName] = sc
}
