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
	WorkDir     string         `json:"workDir,omitempty" yaml:"workDir,omitempty"`         // host volume path
	ExtraConfig string         `json:"extraConfig,omitempty" yaml:"extraConfig,omitempty"` // appended to nextflow config
	Params      map[string]any `json:"params,omitempty" yaml:"params,omitempty"`           // nextflow pipeline params

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
	return base
}

// defaults fills in zero-value fields with sensible defaults.
func (s *PipelineSpec) defaults() {
	if s.WorkDir == "" {
		s.WorkDir = "/work/nextflow-work"
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
