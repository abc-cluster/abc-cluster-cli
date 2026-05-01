package pipeline

import "time"

// PipelineSpec describes a saved or ad-hoc pipeline launch configuration.
// Saved specs are stored in Nomad Variables at nomad/pipelines/<name>.
type PipelineSpec struct {
	// Identity
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Nextflow pipeline source
	Repository string `json:"repository" yaml:"repository"` // e.g. nextflow-io/hello or https://github.com/nf-core/rnaseq
	Revision   string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Profile    string `json:"profile,omitempty" yaml:"profile,omitempty"`   // comma-separated

	// Runtime
	WorkDir     string         `json:"workDir,omitempty" yaml:"workDir,omitempty"`         // host volume path or s3:// URI
	ExtraConfig string         `json:"extraConfig,omitempty" yaml:"extraConfig,omitempty"` // appended to nextflow config
	Params      map[string]any `json:"params,omitempty" yaml:"params,omitempty"`           // nextflow pipeline params
	Resume      bool           `json:"resume,omitempty" yaml:"resume,omitempty"`           // append -resume to nextflow run
	SessionID   string         `json:"sessionID,omitempty" yaml:"sessionID,omitempty"`     // resume specific Nextflow session
	// HostVolume is the Nomad host volume name for shared work storage.
	// Use "-" to disable host volumes (e.g. when workDir is an S3 URI).
	HostVolume     string `json:"hostVolume,omitempty" yaml:"hostVolume,omitempty"`
	// NodeConstraint pins the head job to a specific Nomad node hostname.
	NodeConstraint string `json:"nodeConstraint,omitempty" yaml:"nodeConstraint,omitempty"`
	// PinWorkers, when true with NodeConstraint set, also pins every spawned
	// Nextflow process to the same node (single-host run). Default false:
	// `--node` only pins the head and workers spread across the cluster.
	PinWorkers bool `json:"pinWorkers,omitempty" yaml:"pinWorkers,omitempty"`

	// DevPlugins toggles loading the cluster's nf-abc-cluster-dev meta-plugin
	// bundle into the head container. When set, run.go resolves PluginBundleURL
	// and Plugins from the active context's tools.toml + admin.tools.endpoint.
	DevPlugins bool `json:"devPlugins,omitempty" yaml:"devPlugins,omitempty"`
	// PluginBundleURL is the resolved cluster-side URL of a Nextflow plugin
	// bundle zip. Pulled by the head Nomad task as an artifact and unpacked
	// into $NXF_HOME/plugins. Normally derived from DevPlugins, but can be
	// set explicitly to override or to point at a custom bundle.
	PluginBundleURL string `json:"pluginBundleURL,omitempty" yaml:"pluginBundleURL,omitempty"`
	// Plugins is the ordered list of Nextflow plugin IDs/versions emitted in
	// the generated nextflow.headjob.config plugins { ... } block. Empty means
	// "use the default single nf-nomad@<NfPluginVersion> line".
	Plugins []PluginRef `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	// ExtraBinaries names additional cluster tool binaries the head task
	// should pull as Nomad artifacts (resolved at run time via the active
	// context's tools.toml). Used to satisfy plugin runtime dependencies —
	// e.g. nf-rclone needs the rclone binary on PATH.
	ExtraBinaries []string `json:"extraBinaries,omitempty" yaml:"extraBinaries,omitempty"`

	// Head job resource overrides
	CPU      int    `json:"cpu,omitempty" yaml:"cpu,omitempty"`           // MHz
	MemoryMB int    `json:"memoryMB,omitempty" yaml:"memoryMB,omitempty"` // MB
	NfVersion       string `json:"nfVersion,omitempty" yaml:"nfVersion,omitempty"`
	NfPluginVersion string `json:"nfPluginVersion,omitempty" yaml:"nfPluginVersion,omitempty"`

	// Nomad placement
	Namespace   string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Datacenters []string `json:"datacenters,omitempty" yaml:"datacenters,omitempty"`

	// Record-keeping (set by add/update, not by launch)
	CreatedAt time.Time `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty" yaml:"updatedAt,omitempty"`
}

// PluginRef is one entry in a Nextflow plugins { ... } block. Mirrors
// hclpipeline.PluginRef; converted in hcl_adapter.go.
type PluginRef struct {
	ID      string `json:"id" yaml:"id"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// mergeSpec applies non-zero fields from override on top of base.
func mergeSpec(base, override *PipelineSpec) *PipelineSpec {
	if base == nil {
		base = &PipelineSpec{}
	}
	if override == nil {
		return base
	}
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.Repository != "" {
		base.Repository = override.Repository
	}
	if override.Revision != "" {
		base.Revision = override.Revision
	}
	if override.Profile != "" {
		base.Profile = override.Profile
	}
	if override.WorkDir != "" {
		base.WorkDir = override.WorkDir
	}
	if override.ExtraConfig != "" {
		base.ExtraConfig = override.ExtraConfig
	}
	if len(override.Params) > 0 {
		if base.Params == nil {
			base.Params = map[string]any{}
		}
		for k, v := range override.Params {
			base.Params[k] = v
		}
	}
	if override.CPU != 0 {
		base.CPU = override.CPU
	}
	if override.MemoryMB != 0 {
		base.MemoryMB = override.MemoryMB
	}
	if override.NfVersion != "" {
		base.NfVersion = override.NfVersion
	}
	if override.NfPluginVersion != "" {
		base.NfPluginVersion = override.NfPluginVersion
	}
	if override.Namespace != "" {
		base.Namespace = override.Namespace
	}
	if len(override.Datacenters) > 0 {
		base.Datacenters = append([]string(nil), override.Datacenters...)
	}
	if override.Resume {
		base.Resume = true
	}
	if override.SessionID != "" {
		base.SessionID = override.SessionID
	}
	if override.HostVolume != "" {
		base.HostVolume = override.HostVolume
	}
	if override.NodeConstraint != "" {
		base.NodeConstraint = override.NodeConstraint
	}
	if override.PinWorkers {
		base.PinWorkers = true
	}
	if override.DevPlugins {
		base.DevPlugins = true
	}
	if override.PluginBundleURL != "" {
		base.PluginBundleURL = override.PluginBundleURL
	}
	if len(override.Plugins) > 0 {
		base.Plugins = append([]PluginRef(nil), override.Plugins...)
	}
	if len(override.ExtraBinaries) > 0 {
		base.ExtraBinaries = append([]string(nil), override.ExtraBinaries...)
	}
	return base
}

// isS3URI returns true when the path begins with s3:// or s3a://.
func isS3URI(path string) bool {
	return len(path) > 5 && (path[:5] == "s3://" || (len(path) > 6 && path[:6] == "s3a://"))
}

// defaults fills in zero-value fields with sensible defaults.
func (s *PipelineSpec) defaults() {
	if s.WorkDir == "" {
		s.WorkDir = "/work/nextflow-work"
	}
	// When work dir is S3, disable the host volume unless explicitly set.
	if s.HostVolume == "" && isS3URI(s.WorkDir) {
		s.HostVolume = "-"
	}
	if s.CPU == 0 {
		s.CPU = 1000
	}
	if s.MemoryMB == 0 {
		s.MemoryMB = 2048
	}
	if s.NfVersion == "" {
		s.NfVersion = "25.10.4"
	}
	if s.NfPluginVersion == "" {
		s.NfPluginVersion = "0.4.0-edge3"
	}
	if s.Namespace == "" {
		s.Namespace = "default"
	}
	if len(s.Datacenters) == 0 {
		s.Datacenters = []string{"dc1"}
	}
}
