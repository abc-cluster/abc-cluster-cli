package job

import (
	"os"
	"strconv"
)

// nomadConstraint holds a simple Nomad constraint item.
type nomadConstraint struct {
	Attribute string
	Operator  string
	Value     string
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
	Priority           int
	Nodes              int
	Cores              int
	MemoryMB           int
	GPUs               int
	WalltimeSecs       int
	ChDir              string
	Depend             string
	Driver             string
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

	// ── HPC compatibility env layer ───────────────────────────────────────────
	IncludeHPCCompatEnv bool

	// ── Meta directives ───────────────────────────────────────────────────────
	Meta  map[string]string
	Conda string
	Pixi  bool

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
