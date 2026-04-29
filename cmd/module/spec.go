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

	// TaskDriver is the Nomad task driver for both prestart and run tasks.
	// Default: docker. Use "containerd-driver" to run on nodes that have
	// nomad-driver-containerd registered (e.g. the aither lab node). The same
	// driver is also written into the generated cluster nextflow.config so
	// nf-nomad emits child Nomad jobs with the matching driver.
	TaskDriver string

	// NfPluginZipURL: when set, the run task downloads this zip and unpacks
	// it into NXF_PLUGINS_DIR before invoking nextflow. Used to inject a
	// custom-built nf-nomad plugin (e.g. one with a taskDriver patch) without
	// going through the public Nextflow plugin registry.
	NfPluginZipURL string

	PipelineGenRepo    string
	PipelineGenVersion string
	// PipelineGenURLBase, when set, makes the prestart fetch the JAR from
	// <base>/<version>/pipeline-gen.jar (with sha256 verification against
	// <base>/<version>/sha256sums.txt) instead of GitHub releases. Mirrors the
	// abc-node-probe RustFS-mirror pattern; lets the cluster avoid GitHub rate
	// limits and lets devs ship local builds without cutting a release.
	PipelineGenURLBase string

	// PipelineGenURLResolve is a curl --resolve override (host:port:ip) used
	// when the URL hostname is only resolvable via Tailscale magicDNS on the
	// host but the container's resolv.conf doesn't include 100.100.100.100.
	// Auto-populated by the CLI from a local DNS lookup if the URL hostname
	// is non-numeric.
	PipelineGenURLResolve string
	ModuleRevision     string
	GitHubToken        string

	CPU      int
	MemoryMB int

	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	S3Endpoint string
	// S3AccessKey / S3SecretKey are written into the run task's env block
	// (alongside NF_S3_ENDPOINT) and consumed by the cluster nextflow.config.
	// No Nomad Variable lookup, no hardcoded defaults in the driver — every
	// run is explicit about what creds it ships with.
	S3AccessKey string
	S3SecretKey string

	ParamsYAMLContent string
	ConfigYAMLContent string

	// SamplesheetCSVContent is the user-supplied CSV (read from the path
	// passed to --samplesheet on `module run`). When non-empty, the prestart
	// task validates it against the module's meta.yml via
	// `pipeline-gen --validate-samplesheet` BEFORE generating a driver, so
	// a malformed sheet fails the alloc fast instead of after a long
	// driver-gen + Nextflow pull-through.
	SamplesheetCSVContent string
	// SamplesheetSourcePath is the local path the CSV came from — kept on
	// the spec only for human-readable status output. Not propagated to the
	// cluster; the bytes travel via the spec's CSV content field.
	SamplesheetSourcePath string

	// PipelineGenNoRunManifest passes --no-run-manifest to nf-pipeline-gen in the Nomad prestart script.
	PipelineGenNoRunManifest bool

	// TestMode runs the module against its own bundled tests/main.nf.test fixtures,
	// staged from nf-core/test-datasets at runtime. Forces the "test" profile and
	// suppresses placeholder params (the JAR's generated test profile drives inputs).
	TestMode bool
}

// PublicTestBucketURL is the cluster-side anonymous-RW S3 bucket for module
// test outputs. Allows --test runs to publishDir using a single shared key
// pair shipped via the run task's env block.
const PublicTestBucketURL = "s3://nf-modules-tests"

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
	if s.TaskDriver == "" {
		s.TaskDriver = "docker"
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
			// Test mode: publish to the cluster's anonymous-RW public test
			// bucket so no AWS credentials are required and outputs are
			// inspectable by anyone with cluster network reach.
			s.OutputPrefix = PublicTestBucketURL + "/" + s.JobName
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
		s.CPU = 1000
	}
	if s.MemoryMB == 0 {
		// 2 GB is enough for the Nextflow head process driving a single nf-core
		// module — child processes get their own Nomad allocs via nf-nomad.
		// Was 4096; lowering to 2048 ~3x's the per-cluster job concurrency.
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
