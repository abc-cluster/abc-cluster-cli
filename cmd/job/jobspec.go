package job

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// nomadConstraint holds a simple Nomad constraint item.
type nomadConstraint struct {
	Attribute string
	Operator  string
	Value     string
}

// artifactSpec mirrors jobhcl.ArtifactSpec without creating a cross-package import in jobspec.go.
type artifactSpec struct {
	Source      string
	Destination string
	Mode        string
}

// parseArtifactFlagValue splits the inline "url|dest|mode" encoding used by
// abc data download to pass per-artifact destination/mode through a single
// --artifact flag value.  Plain URLs (no pipe) are returned as-is.
func parseArtifactFlagValue(s string) (url, dest, mode string) {
	parts := strings.SplitN(s, "|", 3)
	url = parts[0]
	if len(parts) > 1 {
		dest = parts[1]
	}
	if len(parts) > 2 {
		mode = parts[2]
	}
	return
}

// nomadAffinity holds a simple Nomad affinity item.
type nomadAffinity struct {
	Attribute string
	Operator  string
	Value     string
	Weight    int
}

// jobSpec holds the configuration for a Nomad batch job derived from
// #ABC/#NOMAD preamble directives, NOMAD_* env vars, CLI flags, and a
// params file. Fields are split into three logical classes:
//
//   - Scheduler: placement — Region, Datacenters, Priority, resources…
//   - Runtime-exposure: boolean flags that inject NOMAD_* vars into the
//     task env block so the script can read them at execution time.
//   - Meta: arbitrary key-value pairs forwarded through Nomad's meta block,
//     readable inside the script as NOMAD_META_<KEY> (key uppercased).
type jobSpec struct {
	// ── Scheduler directives ─────────────────────────────────────────────────
	Name               string
	Namespace          string
	Region             string
	Datacenters        []string
	NodePool           string // Nomad node-pool the job must land in. Empty = Nomad "default"; on multi-pool clusters this fails to place.
	Priority           int
	Nodes              int
	Cores              int
	MemoryMB           int
	GPUs               int
	WalltimeSecs       int
	ChDir              string
	Depend             string
	Driver             string
	// Shell overrides the script interpreter: "bash" → /bin/bash, "sh" →
	// /bin/sh, "" → driver-default (sh for OCI drivers, bash for host
	// drivers). Wired from `--shell` CLI flag / #ABC --shell= preamble.
	Shell              string
	DriverConfig       map[string]string
	RescheduleMode     string
	RescheduleAttempts int
	RescheduleInterval string
	RescheduleDelay    string
	RescheduleMaxDelay string
	OutputLog          string
	ErrorLog           string
	NoNetwork          bool
	Constraints        []nomadConstraint
	Affinities         []nomadAffinity

	// ── Slurm driver directives ───────────────────────────────────────────────
	SlurmPartition  string
	SlurmAccount    string
	SlurmWorkDir    string
	SlurmStdoutFile string
	SlurmStderrFile string
	SlurmNTasks     int
	SlurmReservation string
	SlurmExtraArgs  []string

	// pbsDetected is set by resolveSpecRaw when #PBS directives were found and
	// applied; used by applySpecDefaults to auto-select the "pbs" driver.
	pbsDetected bool

	// ── Placement spread ─────────────────────────────────────────────────────
	// Spread emits a Nomad spread stanza on ${node.unique.id} requesting
	// at-most-one allocation per node (best-effort; Nomad may still bin-pack
	// when eligible nodes < group count).
	Spread bool

	// ── HPC compatibility env layer ───────────────────────────────────────────
	IncludeHPCCompatEnv bool

	// ── Meta directives ───────────────────────────────────────────────────────
	Meta  map[string]string
	Conda string
	Pixi  bool

	// Runtime is a software-stack provisioner (orthogonal to Nomad --driver).
	// From is a backend-native definition path/URI (pixi-exec: path to pixi.toml).
	Runtime string
	From    string

	// PixiBinaryURL is the base URL for the pixi binary (without platform suffix).
	// When non-empty, the wrapper downloads pixi via curl at job start instead of
	// relying on a Nomad artifact stanza.
	PixiBinaryURL string
	// PixiManifestContent holds the content of a local pixi.toml read at submit
	// time. When non-empty, the manifest is embedded as a Nomad template stanza
	// and pixi is fetched as an artifact from the cluster tools storage.
	PixiManifestContent string
	// PixiLockContent holds the content of a pixi.lock file when --from points to
	// a .lock file. Embedded alongside PixiManifestContent; enables --locked install.
	PixiLockContent string
	// PixiInstallLocked passes --locked to `pixi install` for bit-for-bit
	// reproducibility when a pixi.lock file is embedded.
	PixiInstallLocked bool
	// PixiCleanup removes the pixi environment from the task directory on exit.
	PixiCleanup bool

	// MicromambaEnvContent holds the content of a conda environment.yml read at
	// submit time. Embedded as a Nomad template at local/environment.yml.
	MicromambaEnvContent string
	// MicromambaBinaryURL is the base URL for the micromamba binary (without
	// platform suffix). The wrapper downloads micromamba via curl at job start.
	MicromambaBinaryURL string
	// MicromambaCleanup removes the micromamba env from the task directory on exit.
	MicromambaCleanup bool

	// TaskTmp enables task-local temp defaults (TMPDIR under NOMAD_TASK_DIR/tmp).
	TaskTmp bool

	// ── Wave-exec runtime fields ──────────────────────────────────────────────────
	// Set by resolveWaveLocalMode after calling the Wave CLI at submit time.
	//
	// WaveTargetImage is the resolved Wave container URI. It is injected into
	// DriverConfig["image"] so the main task uses the Wave-built image. A Nomad
	// prestart task calls `wave --await` to block until the image is pullable.
	WaveTargetImage string // resolved Wave image URI (e.g. wave.seqera.io/wt/<token>/…)
	WaveEnvContent  string // embedded environment.yml content for the prestart task
	WaveBinaryURL   string // base URL for wave-cli binary (platform suffix added by Nomad)
	WavePlatform    string // target platform, e.g. "linux/amd64" (default)
	WaveTokenSecret string // "nomad/path:key" for TOWER_ACCESS_TOKEN

	// WaveInjectTools lists tool names to inject into the container image via
	// Wave layer augmentation. The special value "*" means all wave_inject tools
	// from tools.toml. Resolved at submit time by resolveWaveInjectMode.
	WaveInjectTools []string

	// ── Data-staging fields (spec abc-job-data-staging-and-run-tags Part A) ───
	// Populated by resolveStaging from the --in/--out flags + the jobstage Plan.
	// When StageEnabled, the HCL generator emits prestart stage-in + poststop
	// stage-out s5cmd tasks (see internal/hclgen/job.StagingSpec).
	StageEnabled          bool
	StageInManifest       string
	StageOutManifest      string
	StageDestRoot         string // alloc-shared CWD, e.g. "$NOMAD_ALLOC_DIR/data/<run>"
	StageS5cmdPath        string
	StageHostVolumeName   string
	StageHostVolumeSource string
	StageHostVolumeMount  string
	StageEnv              map[string]string
	// StageS5cmdSkipTLS makes the stage-in/stage-out s5cmd invocations skip TLS
	// verification (--no-verify-ssl). Required on abc-seedling where MinIO uses a
	// private CA the stage task container does not trust. Detected from the
	// resolved endpoint being HTTPS (mirrors cmd/pipeline/hcl_adapter.go).
	StageS5cmdSkipTLS bool

	// Mounts are host-volume mounts attached to the main task (from --volume /
	// --tools). They let a job reach node-provided tools (the abc-tools volume:
	// s5cmd, mc, …) or data volumes, which an exec task cannot otherwise see.
	Mounts []mountSpec

	// ── Debug / interactive directives ───────────────────────────────────────
	// DebugSleepSecs injects a `sleep N` at the start of the job script so the
	// user can exec into the running allocation to inspect state or attach a
	// debugger before the real workload begins.  Set via --sleep on the CLI.
	DebugSleepSecs int

	// ── Network directives ────────────────────────────────────────────────────
	Ports []string

	// ── Runtime-exposure boolean flags ────────────────────────────────────────
	ExposeAllocID      bool
	ExposeShortAllocID bool
	ExposeAllocName    bool
	ExposeAllocIndex   bool
	ExposeJobID        bool
	ExposeJobName      bool
	ExposeParentJobID  bool
	ExposeGroupName    bool
	ExposeTaskName     bool
	ExposeNamespaceEnv bool
	ExposeDCEnv        bool
	ExposeCPULimit     bool
	ExposeCPUCores     bool
	ExposeMemLimit     bool
	ExposeMemMaxLimit  bool
	ExposeAllocDir     bool
	ExposeTaskDir      bool
	ExposeSecretsDir   bool

	// Artifacts lists remote files Nomad should fetch before the task starts.
	// Populated by the --artifact CLI flag (data download path only).
	Artifacts []artifactSpec
}

// mountSpec is one host-volume mount request for the main task (--volume/--tools).
type mountSpec struct {
	Volume   string // registered host_volume name (e.g. "abc-tools")
	Dest     string // in-task mount path (e.g. "/opt/abc-tools")
	ReadOnly bool
}

// parseVolumeFlag parses a --volume value of the form
// "<name>[:<dest>][:ro|:rw]" into a mountSpec. When <dest> is omitted it
// defaults to "/mnt/<name>"; the mount is read-write unless ":ro" is given.
func parseVolumeFlag(s string) (mountSpec, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return mountSpec{}, fmt.Errorf("--volume %q: empty volume name (use <name>[:<dest>][:ro])", s)
	}
	m := mountSpec{Volume: name, Dest: "/mnt/" + name}
	rest := parts[1:]
	if n := len(rest); n > 0 {
		switch strings.ToLower(strings.TrimSpace(rest[n-1])) {
		case "ro":
			m.ReadOnly = true
			rest = rest[:n-1]
		case "rw":
			rest = rest[:n-1]
		}
	}
	if len(rest) > 0 {
		if dest := strings.TrimSpace(rest[0]); dest != "" {
			if !strings.HasPrefix(dest, "/") {
				return mountSpec{}, fmt.Errorf("--volume %q: mount dest %q must be an absolute path", s, dest)
			}
			m.Dest = dest
		}
	}
	return m, nil
}

// readNomadEnvVars seeds a jobSpec from NOMAD_* environment variables present
// at CLI invocation time. These are the lowest-priority directive source.
func readNomadEnvVars() *jobSpec {
	spec := &jobSpec{}
	spec.Name = os.Getenv("NOMAD_JOB_NAME")
	spec.Namespace = os.Getenv("NOMAD_NAMESPACE")
	if v := os.Getenv("NOMAD_GROUP_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.Nodes = n
		}
	}
	if v := os.Getenv("NOMAD_CPU_CORES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.Cores = n
		}
	}
	if v := os.Getenv("NOMAD_MEMORY_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.MemoryMB = n
		}
	}
	return spec
}

// mergeSpec returns base with non-zero fields from override applied on top.
// Boolean expose flags use "true wins" semantics — once set they are not
// cleared by a lower-priority source.
func mergeSpec(base, override *jobSpec) *jobSpec {
	if base == nil {
		base = &jobSpec{}
	}
	if override == nil {
		return base
	}
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Namespace != "" {
		base.Namespace = override.Namespace
	}
	if override.Region != "" {
		base.Region = override.Region
	}
	if len(override.Datacenters) > 0 {
		base.Datacenters = append([]string(nil), override.Datacenters...)
	}
	if override.Priority != 0 {
		base.Priority = override.Priority
	}
	if override.Nodes != 0 {
		base.Nodes = override.Nodes
	}
	if override.Cores != 0 {
		base.Cores = override.Cores
	}
	if override.MemoryMB != 0 {
		base.MemoryMB = override.MemoryMB
	}
	if override.GPUs != 0 {
		base.GPUs = override.GPUs
	}
	if override.WalltimeSecs != 0 {
		base.WalltimeSecs = override.WalltimeSecs
	}
	if override.ChDir != "" {
		base.ChDir = override.ChDir
	}
	if override.Depend != "" {
		base.Depend = override.Depend
	}
	if override.Driver != "" {
		base.Driver = override.Driver
	}
	if override.Shell != "" {
		base.Shell = override.Shell
	}
	if override.RescheduleMode != "" {
		base.RescheduleMode = override.RescheduleMode
	}
	if override.RescheduleAttempts != 0 {
		base.RescheduleAttempts = override.RescheduleAttempts
	}
	if override.RescheduleInterval != "" {
		base.RescheduleInterval = override.RescheduleInterval
	}
	if override.RescheduleDelay != "" {
		base.RescheduleDelay = override.RescheduleDelay
	}
	if override.RescheduleMaxDelay != "" {
		base.RescheduleMaxDelay = override.RescheduleMaxDelay
	}
	if override.OutputLog != "" {
		base.OutputLog = override.OutputLog
	}
	if override.ErrorLog != "" {
		base.ErrorLog = override.ErrorLog
	}
	if override.Conda != "" {
		base.Conda = override.Conda
	}
	if override.Runtime != "" {
		base.Runtime = override.Runtime
	}
	if override.From != "" {
		base.From = override.From
	}
	if override.PixiBinaryURL != "" {
		base.PixiBinaryURL = override.PixiBinaryURL
	}
	if override.PixiManifestContent != "" {
		base.PixiManifestContent = override.PixiManifestContent
	}
	if override.PixiLockContent != "" {
		base.PixiLockContent = override.PixiLockContent
	}
	if override.PixiInstallLocked {
		base.PixiInstallLocked = true
	}
	if override.PixiCleanup {
		base.PixiCleanup = true
	}
	if override.MicromambaEnvContent != "" {
		base.MicromambaEnvContent = override.MicromambaEnvContent
	}
	if override.MicromambaBinaryURL != "" {
		base.MicromambaBinaryURL = override.MicromambaBinaryURL
	}
	if override.MicromambaCleanup {
		base.MicromambaCleanup = true
	}
	if override.WaveTargetImage != "" {
		base.WaveTargetImage = override.WaveTargetImage
	}
	if override.WaveEnvContent != "" {
		base.WaveEnvContent = override.WaveEnvContent
	}
	if override.WaveBinaryURL != "" {
		base.WaveBinaryURL = override.WaveBinaryURL
	}
	if override.WavePlatform != "" {
		base.WavePlatform = override.WavePlatform
	}
	if override.WaveTokenSecret != "" {
		base.WaveTokenSecret = override.WaveTokenSecret
	}
	if len(override.WaveInjectTools) > 0 {
		base.WaveInjectTools = append([]string(nil), override.WaveInjectTools...)
	}
	if override.TaskTmp {
		base.TaskTmp = true
	}
	if override.NoNetwork {
		base.NoNetwork = true
	}
	if len(override.Constraints) > 0 {
		base.Constraints = append([]nomadConstraint(nil), override.Constraints...)
	}
	if len(override.Affinities) > 0 {
		base.Affinities = append([]nomadAffinity(nil), override.Affinities...)
	}
	if override.DriverConfig != nil {
		if base.DriverConfig == nil {
			base.DriverConfig = map[string]string{}
		}
		for k, v := range override.DriverConfig {
			base.DriverConfig[k] = v
		}
	}
	if override.Meta != nil {
		if base.Meta == nil {
			base.Meta = map[string]string{}
		}
		for k, v := range override.Meta {
			base.Meta[k] = v
		}
	}
	if len(override.Ports) > 0 {
		base.Ports = append([]string(nil), override.Ports...)
	}
	if override.DebugSleepSecs != 0 {
		base.DebugSleepSecs = override.DebugSleepSecs
	}
	if override.SlurmPartition != "" {
		base.SlurmPartition = override.SlurmPartition
	}
	if override.SlurmAccount != "" {
		base.SlurmAccount = override.SlurmAccount
	}
	if override.SlurmWorkDir != "" {
		base.SlurmWorkDir = override.SlurmWorkDir
	}
	if override.SlurmStdoutFile != "" {
		base.SlurmStdoutFile = override.SlurmStdoutFile
	}
	if override.SlurmStderrFile != "" {
		base.SlurmStderrFile = override.SlurmStderrFile
	}
	if override.SlurmNTasks != 0 {
		base.SlurmNTasks = override.SlurmNTasks
	}
	if override.SlurmReservation != "" {
		base.SlurmReservation = override.SlurmReservation
	}
	if len(override.SlurmExtraArgs) > 0 {
		base.SlurmExtraArgs = append([]string(nil), override.SlurmExtraArgs...)
	}
	if override.pbsDetected {
		base.pbsDetected = true
	}
	if override.Spread {
		base.Spread = true
	}
	if override.IncludeHPCCompatEnv {
		base.IncludeHPCCompatEnv = true
	}
	if override.Pixi {
		base.Pixi = true
	}
	// Boolean expose flags: true wins.
	if override.ExposeAllocID {
		base.ExposeAllocID = true
	}
	if override.ExposeShortAllocID {
		base.ExposeShortAllocID = true
	}
	if override.ExposeAllocName {
		base.ExposeAllocName = true
	}
	if override.ExposeAllocIndex {
		base.ExposeAllocIndex = true
	}
	if override.ExposeJobID {
		base.ExposeJobID = true
	}
	if override.ExposeJobName {
		base.ExposeJobName = true
	}
	if override.ExposeParentJobID {
		base.ExposeParentJobID = true
	}
	if override.ExposeGroupName {
		base.ExposeGroupName = true
	}
	if override.ExposeTaskName {
		base.ExposeTaskName = true
	}
	if override.ExposeNamespaceEnv {
		base.ExposeNamespaceEnv = true
	}
	if override.ExposeDCEnv {
		base.ExposeDCEnv = true
	}
	if override.ExposeCPULimit {
		base.ExposeCPULimit = true
	}
	if override.ExposeCPUCores {
		base.ExposeCPUCores = true
	}
	if override.ExposeMemLimit {
		base.ExposeMemLimit = true
	}
	if override.ExposeMemMaxLimit {
		base.ExposeMemMaxLimit = true
	}
	if override.ExposeAllocDir {
		base.ExposeAllocDir = true
	}
	if override.ExposeTaskDir {
		base.ExposeTaskDir = true
	}
	if override.ExposeSecretsDir {
		base.ExposeSecretsDir = true
	}
	return base
}
