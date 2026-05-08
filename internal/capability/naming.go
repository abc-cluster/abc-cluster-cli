package capability

// Codename returns the human-friendly codename for a technical service
// name (e.g. "abc-fleet-svc" → "Veld"), or "" if the service has no
// codename. The empty return triggers "no parens" rendering per the
// naming convention.
//
// Mirrors design/exploring/service-naming-map.md. Update when a new
// service lands in the naming map.
func Codename(techName string) string {
	if cn, ok := codenameTable[techName]; ok {
		return cn
	}
	return ""
}

// FormatService renders a service identifier following the naming
// convention: technical name first, codename in parentheses if present.
// Used for error messages, banners, and `abc cluster capabilities show`.
//
// Examples:
//
//	FormatService("abc-fleet-svc")    → "abc-fleet-svc (Veld)"
//	FormatService("abc-data-api")     → "abc-data-api"  (no codename)
//	FormatService("local-state")      → "local-state"   (no codename)
func FormatService(techName string) string {
	if cn := Codename(techName); cn != "" {
		return techName + " (" + cn + ")"
	}
	return techName
}

// codenameTable is the canonical map. Generated from
// design/exploring/service-naming-map.md at release time. Do not edit
// without also updating the naming-map doc — the map and the doc must
// stay in lockstep so documentation, glossary, and CLI output agree.
var codenameTable = map[string]string{
	// Core platform services
	"abc-controller-svc": "Khan",
	"abc-policy-svc":     "Jurist",
	"abc-client-web":     "Khatoon",

	// Bitemporal substrate + consumer services
	"abc-bitemporal-svc":  "Chiranjivi",
	"abc-accounting-svc":  "Kayastha",
	"abc-fleet-svc":       "Veld",
	"abc-chat-svc":        "Mimir",

	// Telemetry, audit, observability
	"abc-telemetry-svc": "Voron",

	// Marketplace + business services
	"abc-marketplace-svc": "Bazaar",
	"abc-billing-bridge":  "Hisaab",

	// Storage + data-plane
	"abc-archive-svc": "Garage",

	// Codename-less services (intentionally absent from the table):
	//   abc-data-api        — folded into abc-bitemporal-svc 2026-05-08
	//   abc-energy-collector
	//   abc-node-probe
	//   abc-metrics-svc
	//   abc-signup-svc
	//   abc-minio-svc
	//   abc-tailscale-svc
	//   abc-keycloak-svc
	//   abc-identity-seed
	//   local-state         — pseudo-service for the local SQLite
}

// NomadJobToService maps observed Nomad job names to the canonical
// service identity used in the Capabilities.Services map. Includes
// backward-compat aliases for renamed / merged services so old
// deployments still resolve correctly during the migration window.
//
// Mirrors design/exploring/service-naming-map.md and §"Discovery
// cascade — Nomad-job introspection map" of the capability brainstorm.
//
// Used by `abc cluster capabilities sync` when probing Nomad's services
// API or job listing (the pre-Khan / seedling probe path).
var NomadJobToService = map[string]string{
	// Canonical names
	"abc-policy-svc":      "abc-policy-svc",
	"abc-controller-svc":  "abc-controller-svc",
	"abc-bitemporal-svc":  "abc-bitemporal-svc",
	"abc-fleet-svc":       "abc-fleet-svc",
	"abc-accounting-svc":  "abc-accounting-svc",
	"abc-chat-svc":        "abc-chat-svc",
	"abc-telemetry-svc":   "abc-telemetry-svc",
	"abc-client-web":      "abc-client-web",
	"abc-marketplace-svc": "abc-marketplace-svc",
	"abc-billing-bridge":  "abc-billing-bridge",
	"abc-signup-svc":      "abc-signup-svc",

	// Currently-deployed live-on-aither names (per CLI reference §7)
	"abc-experimental-xtdb":   "abc-bitemporal-svc",
	"abc-experimental-jurist": "abc-policy-svc",
	"abc-grove-xtdb":          "abc-bitemporal-svc",

	// Backward-compat aliases (renames + folds, 2026-05-08)
	"abc-data-api":           "abc-bitemporal-svc",
	"abc-grid-intensity-svc": "abc-accounting-svc",
	"abc-embodied-svc":       "abc-accounting-svc",
	"abc-jurist-svc":         "abc-policy-svc",
	"abc-khan-svc":           "abc-controller-svc",
	"abc-khatoon-web":        "abc-client-web",
	"abc-sanctum-token-svc":  "abc-controller-svc",
}
