package module

import "strings"

// RunSpec describes an "abc module run" request.
type RunSpec struct {
	JobName string
	Module  string

	Profile      string
	WorkDir      string
	HostVolume   string
	OutputPrefix string

	PipelineGenRepo    string
	PipelineGenVersion string
	// PipelineGenURLBase, when set, makes the prestart fetch the JAR from
	// <base>/<version>/pipeline-gen.jar (with sha256 verification against
	// <base>/<version>/sha256sums.txt) instead of GitHub releases. Mirrors the
	// abc-node-probe RustFS-mirror pattern; lets the cluster avoid GitHub rate
	// limits and lets devs ship local builds without cutting a release.
	PipelineGenURLBase string
	ModuleRevision     string
	GitHubToken        string

	CPU      int
	MemoryMB int

	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	S3Endpoint string

	ParamsYAMLContent string
	ConfigYAMLContent string

	// PipelineGenNoRunManifest passes --no-run-manifest to nf-pipeline-gen in the Nomad prestart script.
	PipelineGenNoRunManifest bool

	// TestMode runs the module against its own bundled tests/main.nf.test fixtures,
	// staged from nf-core/test-datasets at runtime. Forces the "test" profile and
	// suppresses placeholder params (the JAR's generated test profile drives inputs).
	TestMode bool
}

func (s *RunSpec) defaults() {
	if s.JobName == "" {
		s.JobName = defaultJobName(s.Module)
	}
	if s.Profile == "" {
		s.Profile = "test"
	}
	if s.TestMode && !profileContains(s.Profile, "test") {
		s.Profile = s.Profile + ",test"
	}
	if s.HostVolume == "" {
		// `scratch` is registered on every node by `abc compute add` defaults
		// (path /opt/nomad/scratch). The legacy `nextflow-work` volume only
		// existed on one GCP node. Defaulting to scratch lets module runs
		// schedule anywhere in the cluster.
		s.HostVolume = "scratch"
	}
	if s.WorkDir == "" {
		s.WorkDir = "/opt/nomad/scratch/nextflow-work"
	}
	if s.OutputPrefix == "" {
		if s.TestMode {
			// Test mode: keep outputs on the shared host volume so the run
			// works against any cluster without requiring a pre-existing
			// S3 bucket or S3 endpoint configuration.
			s.OutputPrefix = s.WorkDir + "/test-outputs"
		} else {
			s.OutputPrefix = "s3://user-output/nextflow"
		}
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

func profileContains(profileList, profile string) bool {
	for _, p := range strings.Split(profileList, ",") {
		if strings.TrimSpace(p) == profile {
			return true
		}
	}
	return false
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
