package module

import "strings"

// RunSpec describes an "abc module run" request.
type RunSpec struct {
	JobName string
	Module  string

	Profile      string
	WorkDir      string
	OutputPrefix string

	PipelineGenRepo    string
	PipelineGenVersion string
	ModuleRevision     string
	GitHubToken        string

	CPU      int
	MemoryMB int

	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	MinioEndpoint string

	ParamsYAMLContent string
	ConfigYAMLContent string

	// PipelineGenNoRunManifest passes --no-run-manifest to nf-pipeline-gen in the Nomad prestart script.
	PipelineGenNoRunManifest bool
}

func (s *RunSpec) defaults() {
	if s.JobName == "" {
		s.JobName = defaultJobName(s.Module)
	}
	if s.Profile == "" {
		s.Profile = "nomad,test"
	}
	if s.WorkDir == "" {
		s.WorkDir = "/work/nextflow-work"
	}
	if s.OutputPrefix == "" {
		s.OutputPrefix = "s3://user-output/nextflow"
	}
	if s.PipelineGenRepo == "" {
		s.PipelineGenRepo = "abc-cluster/nf-pipeline-gen"
	}
	if s.PipelineGenVersion == "" {
		s.PipelineGenVersion = "latest"
	}
	if s.CPU == 0 {
		s.CPU = 1500
	}
	if s.MemoryMB == 0 {
		s.MemoryMB = 4096
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

func defaultJobName(moduleName string) string {
	slug := moduleSlug(moduleName)
	if slug == "" {
		return "module-run"
	}
	return "module-" + slug
}

func moduleSlug(moduleName string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(moduleName) {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
