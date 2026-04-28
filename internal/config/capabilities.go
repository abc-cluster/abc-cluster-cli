package config

import "time"

// Capabilities describes which services are detected on an abc-nodes cluster.
// Populated by "abc cluster capabilities sync". Treat as read-only in all other commands.
type Capabilities struct {
	Storage       string    `yaml:"storage,omitempty"`       // minio | rustfs | none
	Uploads       bool      `yaml:"uploads,omitempty"`       // tusd running
	UploadUI      bool      `yaml:"upload_ui,omitempty"`     // uppy running
	Logging       bool      `yaml:"logging,omitempty"`       // loki running
	Monitoring    bool      `yaml:"monitoring,omitempty"`    // prometheus running
	Observability bool      `yaml:"observability,omitempty"` // alloy running
	Notifications bool      `yaml:"notifications,omitempty"` // ntfy running
	Secrets       string    `yaml:"secrets,omitempty"`       // nomad | vault | vault+sealed | none
	Proxy         bool      `yaml:"proxy,omitempty"`         // traefik running
	// Nodes lists per-node driver capabilities. Updated by "abc cluster capabilities sync".
	Nodes      []NodeCapability `yaml:"nodes,omitempty"`
	LastSynced time.Time        `yaml:"last_synced,omitempty"`
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
