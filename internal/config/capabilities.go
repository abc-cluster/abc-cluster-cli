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
	LastSynced    time.Time `yaml:"last_synced,omitempty"`
}
