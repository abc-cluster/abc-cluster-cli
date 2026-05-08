package config

import "time"

// Capabilities describes which services are detected on an abc-nodes cluster.
// Populated by "abc cluster capabilities sync". Treat as read-only in all other commands.
type Capabilities struct {
	// ── abc-nodes-tier shorthand booleans (legacy / seedling) ──
	Storage       string `yaml:"storage,omitempty"`       // minio | rustfs | none
	Uploads       bool   `yaml:"uploads,omitempty"`       // tusd running
	UploadUI      bool   `yaml:"upload_ui,omitempty"`     // uppy running
	Logging       bool   `yaml:"logging,omitempty"`       // loki running
	Monitoring    bool   `yaml:"monitoring,omitempty"`    // prometheus running
	Observability bool   `yaml:"observability,omitempty"` // alloy running
	Notifications bool   `yaml:"notifications,omitempty"` // ntfy running
	Secrets       string `yaml:"secrets,omitempty"`       // nomad | vault | vault+sealed | none
	Proxy         bool   `yaml:"proxy,omitempty"`         // traefik running
	// Nodes lists per-node driver capabilities. Updated by "abc cluster capabilities sync".
	Nodes      []NodeCapability `yaml:"nodes,omitempty"`
	LastSynced time.Time        `yaml:"last_synced,omitempty"`

	// ── per-service-with-version-and-features model (Stage A 2026-05-08) ──
	// Per `brainstorms/cli-capability-discovery/2026-05-08-capability-probe-and-version-skew.md`.
	// All `omitempty`: existing config.yaml files load fine without these fields;
	// next `abc cluster capabilities sync` populates them.
	SchemaVersion int                          `yaml:"schema_version,omitempty"`
	Services      map[string]ServiceCapability `yaml:"services,omitempty"`
	ProbeSource   string                       `yaml:"probe_source,omitempty"`   // khan-aggregate | pulumi-snapshot | nomad-introspection | config-pin | tier-default
	ProbeWarnings []string                     `yaml:"probe_warnings,omitempty"`
}

// ServiceCapability is one service's entry in the Capabilities.Services map.
// Keyed by technical name (e.g. "abc-bitemporal-svc"); the codename
// (e.g. "Chiranjivi") is in the Codename field for pretty-print rendering.
type ServiceCapability struct {
	Codename           string                     `yaml:"codename,omitempty"`
	Available          bool                       `yaml:"available"`
	Version            string                     `yaml:"version,omitempty"`             // semver, or migration name for local-state
	Features           []string                   `yaml:"features,omitempty"`
	DeprecatedFeatures map[string]DeprecationInfo `yaml:"deprecated_features,omitempty"`
	Endpoints          map[string]string          `yaml:"endpoints,omitempty"`           // e.g. {"http": "...", "pgwire": "..."}
	Reason             string                     `yaml:"reason,omitempty"`              // when available=false
	Fallback           string                     `yaml:"fallback,omitempty"`            // hint when degraded
}

// DeprecationInfo describes a feature on a sunset path.
type DeprecationInfo struct {
	RemovedIn   string    `yaml:"removed_in,omitempty"`
	Replacement string    `yaml:"replacement,omitempty"`
	SunsetDate  time.Time `yaml:"sunset_date,omitempty"`
}

// NodeCapability records the driver capabilities of a single Nomad client node,
// as reported by GET /v1/node/<id>. Populated by "abc cluster capabilities sync".
//
// The optional Probe field carries the most recent abc-node-probe JSON output
// for this node, populated by "abc cluster configuration sync --id <node-id>".
// Driver/volume metadata above remains the cheap-to-refresh "what's running"
// view; Probe is the deep "what's the hardware/OS/security posture" snapshot.
type NodeCapability struct {
	ID       string           `yaml:"id"`
	Hostname string           `yaml:"hostname"`
	Drivers  []string         `yaml:"drivers,omitempty"` // healthy+detected drivers only
	Volumes  []string         `yaml:"volumes,omitempty"` // host volumes: "name:/path" or "name:/path (ro)"
	Probe    *NodeProbeReport `yaml:"probe,omitempty"`   // latest abc-node-probe report
}

// NodeProbeReport is a structured wrapper around the JSON output of
// abc-node-probe for one node, plus a few pre-extracted fields for quick
// lookup without re-parsing Raw. Populated by
// "abc cluster configuration sync --id <node-id>".
type NodeProbeReport struct {
	CollectedAt time.Time `yaml:"collected_at"`           // when the probe ran
	ProbeVersion string   `yaml:"probe_version,omitempty"` // GitHub release tag fetched (e.g. v0.1.4)
	Severity    string    `yaml:"severity,omitempty"`     // PASS | WARN | FAIL | INFO (highest seen)
	Jurisdiction string   `yaml:"jurisdiction,omitempty"` // ISO-3166 alpha-2 if probe was given --jurisdiction
	// Raw is the entire abc-node-probe JSON output for this node, preserved for
	// forward compatibility — abc-node-probe's schema is the source of truth and
	// new fields appear release to release.
	Raw map[string]interface{} `yaml:"raw,omitempty"`
}
