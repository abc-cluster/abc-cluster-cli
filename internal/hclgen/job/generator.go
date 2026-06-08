package job

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	shellquote "github.com/kballard/go-shellquote"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// shellQuote safely shell-quotes s as a single token suitable for use as a
// shell -c argument or inside a compound shell command.
func shellQuote(s string) string {
	return shellquote.Join(s)
}

// setAttributeRawString emits a string attribute that preserves runtime
// `${...}` interpolations literally. cty.StringVal escapes `${` to `$${`,
// which Nomad reads as the literal sequence and skips interpolation —
// breaking artifact source URLs that depend on `${attr.kernel.name}` etc.
// Writing raw quoted bytes bypasses the escape pass.
func setAttributeRawString(body *hclwrite.Body, name, value string) {
	body.SetAttributeRaw(name, hclwrite.Tokens{
		{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(value)},
		{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	})
}

// parseDriverConfigList recognises a JSON-array-of-strings value (e.g.
// `["--cpu","1","--timeout","5s"]`) used in #ABC/#NOMAD driver.config
// directives for list-typed fields like docker's `args` or `volumes`.
//
// The directive parser splits on whitespace via strings.Fields, so the value
// must not contain spaces — JSON arrays with quoted elements work because
// `["a","b"]` is a single whitespace-free token.
//
// Returns the parsed list and ok=true on success; ok=false means the value
// is not a JSON array and the caller should fall back to a string value.
func parseDriverConfigList(v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, false
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, false
	}
	return out, true
}

type Constraint struct {
	Attribute string
	Operator  string
	Value     string
}

type Affinity struct {
	Attribute string
	Operator  string
	Value     string
	Weight    int
}

// ArtifactSpec describes a single Nomad artifact stanza: a remote file that
// Nomad fetches into the task directory before the task starts.
type ArtifactSpec struct {
	// Source is the URL of the file to fetch (http/https/s3/etc.).
	Source string
	// Destination is relative to the task directory; defaults to "local/" when empty.
	Destination string
	// Mode is "file", "dir", or "any"; empty uses Nomad's default.
	Mode string
}

// TemplateSpec describes an additional Nomad template stanza beyond the job script.
// Used to stage supporting files (e.g. a pixi.toml manifest) into the task dir.
type TemplateSpec struct {
	Data        string
	Destination string
	Perms       string // e.g. "0644"; empty uses Nomad's default
}

type Spec struct {
	Name               string
	Namespace          string
	Region             string
	Datacenters        []string
	NodePool           string // Nomad node-pool; emits `node_pool = "..."` at job scope when non-empty.
	Priority           int
	Nodes              int
	Cores              int
	MemoryMB           int
	GPUs               int
	WalltimeSecs       int
	ChDir              string
	Depend             string
	Driver             string
	// Shell overrides the script interpreter. "" (default) picks per
	// `pickTaskScriptShell`. Accepted forms:
	//   - "" (default) → driver-default (/bin/sh for OCI drivers, /bin/bash for host drivers)
	//   - "bash" / "sh" / any bare name → resolved to /bin/<name>
	//   - absolute path "/usr/local/bin/fish" → used verbatim
	// Wired from #ABC --shell=… preamble or --shell CLI flag.
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
	Constraints        []Constraint
	Affinities         []Affinity

	SlurmPartition  string
	SlurmAccount    string
	SlurmWorkDir    string
	SlurmStdoutFile string
	SlurmStderrFile string
	SlurmNTasks     int
	SlurmReservation string
	SlurmExtraArgs  []string

	// Spread emits a Nomad spread stanza requesting at-most-one allocation per node.
	Spread bool

	IncludeHPCCompatEnv bool

	Meta  map[string]string
	Conda string
	Pixi  bool
	// TaskTmp sets TMPDIR/TMP/TEMP to ${NOMAD_TASK_DIR}/tmp in the task env block.
	TaskTmp bool

	Ports []string

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

	// StaticEnv is merged into the task env block as literal strings (no Nomad
	// interpolation). Used for abc-nodes enhanced floor wiring (Loki, Prometheus, Alloy).
	StaticEnv map[string]string

	// Artifacts lists remote files Nomad fetches into the task directory before
	// the task starts (Nomad artifact stanza). Used by the data download path
	// to stage tool binaries (e.g. s5cmd) on exec driver tasks.
	Artifacts []ArtifactSpec

	// ExtraTemplates lists additional Nomad template stanzas to render into the
	// task directory alongside the job script (e.g. a pixi.toml manifest).
	ExtraTemplates []TemplateSpec

	// Wave holds configuration for the Wave container build prestart task.
	// When Wave.Enabled is true, a prestart task is emitted that calls the Wave
	// CLI with --await, blocking until the container image is pullable before
	// the main task starts.
	Wave WaveSpec

	// Staging holds per-script data-staging configuration (spec
	// abc-job-data-staging-and-run-tags Part A). When Staging.Enabled is true,
	// a group "nf-work" host volume is declared and two s5cmd lifecycle tasks
	// are emitted: a prestart "stage-in" (downloads declared inputs into the
	// alloc-shared DestRoot before the main task) and a poststop "stage-out"
	// (uploads declared outputs after the main task succeeds). The main task is
	// expected to run with CWD = DestRoot so the script's relative paths resolve.
	Staging StagingSpec
}

// StagingSpec configures the s5cmd stage-in / stage-out lifecycle tasks.
type StagingSpec struct {
	// Enabled emits the staging volume + prestart/poststop tasks.
	Enabled bool
	// StageInManifest / StageOutManifest are the declarative `s5cmd run`
	// commands-file bodies (generated by internal/jobstage).
	StageInManifest  string
	StageOutManifest string
	// DestRoot is the alloc-shared mirror root the manifests reference, e.g.
	// "${NOMAD_ALLOC_DIR}/data/<run>" — MUST be under the alloc dir (shared
	// across the prestart/main/poststop tasks), not a task-local /local path.
	DestRoot string
	// S5cmdPath is the in-container path to the s5cmd binary (from the mounted
	// host volume), e.g. "/nxf-work/bin/s5cmd".
	S5cmdPath string
	// Host volume that carries the s5cmd binary (reused from nf-nomad-s5cmd).
	HostVolumeName   string // e.g. "nf-work"
	HostVolumeSource string // host path, e.g. "/opt/abc-seedling/nf-work"
	HostVolumeMount  string // in-container mount path, e.g. "/nxf-work"
	// Env is injected (as a template at secrets/s5cmd.env, env=true) into the
	// stage tasks — MinIO creds + S3 endpoint + AWS_CA_BUNDLE/--no-verify-ssl
	// for the private CA. Values supplied by the wiring layer.
	Env map[string]string
	// SkipTLS adds --no-verify-ssl to the stage-in/stage-out s5cmd invocations.
	// Required on abc-seedling where MinIO uses a private CA the stage task
	// container does not trust (mirrors the pipeline path's useTLS=false).
	SkipTLS bool
}

// WaveSpec configures the Wave container build prestart task.
type WaveSpec struct {
	// Enabled signals that the wave-build prestart task should be emitted.
	Enabled bool
	// CondaFileContent is the content of the conda environment.yml to embed as
	// a template in the prestart task.
	CondaFileContent string
	// TokenSecretPath is the Nomad Variables path holding TOWER_ACCESS_TOKEN
	// (e.g. "nomad/jobs").
	TokenSecretPath string
	// TokenSecretKey is the key within TokenSecretPath (e.g. "wave_token").
	TokenSecretKey string
	// Platform is the target OCI platform (e.g. "linux/amd64").
	Platform string
	// BinarySourceURL is the Nomad artifact source URL for the wave-cli binary,
	// including the Nomad interpolation suffix for os/arch (e.g.
	// "http://rustfs/abc-reserved/binary_tools/wave-${attr.kernel.name}-${attr.cpu.arch}").
	BinarySourceURL string
}

// nomadHCLOperator converts CLI shorthand operators to the operator strings
// that Nomad's HCL job spec accepts. Only =~ needs translation (→ regexp);
// all other operators (==, !=, <, <=, >, >=) are accepted as-is by Nomad.
func nomadHCLOperator(op string) string {
	if op == "=~" {
		return "regexp"
	}
	return op
}

// nomadConstraintAttr ensures a constraint/affinity attribute is wrapped in
// Nomad's interpolation syntax ${...}. Nomad evaluates the attribute string
// as a runtime variable reference; without the ${ } markers it is treated as
// a literal string that never matches any node property.
//
// Examples: "node.unique.name" → "${node.unique.name}"
//           "${attr.cpu.arch}" → "${attr.cpu.arch}"  (already wrapped, no-op)
func nomadConstraintAttr(attr string) string {
	if strings.HasPrefix(attr, "${") {
		return attr
	}
	return "${" + attr + "}"
}

func Generate(spec Spec, scriptName, scriptContent string) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	jobBlock := root.AppendNewBlock("job", []string{spec.Name})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("type", cty.StringVal("batch"))
	jobBody.SetAttributeValue("priority", cty.NumberIntVal(int64(spec.Priority)))
	if spec.Region != "" {
		jobBody.SetAttributeValue("region", cty.StringVal(spec.Region))
	}
	if spec.Namespace != "" {
		jobBody.SetAttributeValue("namespace", cty.StringVal(spec.Namespace))
	}
	if len(spec.Datacenters) > 0 {
		dcs := make([]cty.Value, len(spec.Datacenters))
		for i, dc := range spec.Datacenters {
			dcs[i] = cty.StringVal(dc)
		}
		jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	}
	// node_pool — emitted when non-empty so seedling-shaped clusters (where
	// every node is in a non-default pool) can place this job. Empty leaves
	// Nomad's `default` pool, which is fine on single-pool clusters.
	if spec.NodePool != "" {
		jobBody.SetAttributeValue("node_pool", cty.StringVal(spec.NodePool))
	}
	for _, c := range spec.Constraints {
		b := jobBody.AppendNewBlock("constraint", nil).Body()
		b.SetAttributeValue("attribute", cty.StringVal(nomadConstraintAttr(c.Attribute)))
		b.SetAttributeValue("operator", cty.StringVal(nomadHCLOperator(c.Operator)))
		b.SetAttributeValue("value", cty.StringVal(c.Value))
	}
	for _, a := range spec.Affinities {
		b := jobBody.AppendNewBlock("affinity", nil).Body()
		b.SetAttributeValue("attribute", cty.StringVal(nomadConstraintAttr(a.Attribute)))
		b.SetAttributeValue("operator", cty.StringVal(nomadHCLOperator(a.Operator)))
		b.SetAttributeValue("value", cty.StringVal(a.Value))
		b.SetAttributeValue("weight", cty.NumberIntVal(int64(a.Weight)))
	}
	if len(spec.Meta) > 0 {
		metaBody := jobBody.AppendNewBlock("meta", nil).Body()
		for _, k := range utils.SortedKeys(spec.Meta) {
			metaBody.SetAttributeValue(k, cty.StringVal(spec.Meta[k]))
		}
	}

	groupBody := jobBody.AppendNewBlock("group", []string{"main"}).Body()
	groupBody.SetAttributeValue("count", cty.NumberIntVal(int64(spec.Nodes)))
	// Batch scripts should fail fast; Nomad's default restart policy retries exit
	// failures (several attempts with backoff), delaying terminal job status.
	groupBody.AppendNewBlock("restart", nil).Body().
		SetAttributeValue("attempts", cty.NumberIntVal(0))
	if spec.Spread {
		groupBody.AppendNewBlock("spread", nil).Body().
			SetAttributeValue("attribute", cty.StringVal("${node.unique.id}"))
	}

	if spec.NoNetwork {
		groupBody.AppendNewBlock("network", nil).Body().
			SetAttributeValue("mode", cty.StringVal("none"))
	} else if len(spec.Ports) > 0 {
		netBody := groupBody.AppendNewBlock("network", nil).Body()
		for _, p := range spec.Ports {
			netBody.AppendNewBlock("port", []string{p})
		}
	}

	if spec.Depend != "" {
		waitBody := groupBody.AppendNewBlock("task", []string{"wait-dependency"}).Body()
		waitBody.SetAttributeValue("driver", cty.StringVal(spec.Driver))
		lc := waitBody.AppendNewBlock("lifecycle", nil).Body()
		lc.SetAttributeValue("hook", cty.StringVal("prestart"))
		lc.SetAttributeValue("sidecar", cty.BoolVal(false))
		cfg := waitBody.AppendNewBlock("config", nil).Body()
		cfg.SetAttributeValue("command", cty.StringVal("/bin/sh"))
		cfg.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("-c"),
			cty.StringVal(fmt.Sprintf("echo Waiting for dependency: %s", spec.Depend)),
		}))
	}

	if spec.Wave.Enabled {
		appendWavePrestartTask(groupBody, spec.Wave)
	}

	// Data staging (spec abc-job-data-staging-and-run-tags Part A): declare the
	// host volume carrying s5cmd and emit the prestart stage-in + poststop
	// stage-out s5cmd tasks. Nomad orders tasks by lifecycle hook, not HCL
	// position, so emitting stage-out here (before "main" in the file) is fine.
	if spec.Staging.Enabled {
		vol := groupBody.AppendNewBlock("volume", []string{spec.Staging.HostVolumeName}).Body()
		vol.SetAttributeValue("type", cty.StringVal("host"))
		// `source` is the client-registered host_volume NAME, not its host path
		// (the path lives only in the Nomad client config). HostVolumeSource is
		// retained for documentation but must not be emitted here.
		vol.SetAttributeValue("source", cty.StringVal(spec.Staging.HostVolumeName))
		vol.SetAttributeValue("read_only", cty.BoolVal(true))
		appendStageTask(groupBody, "stage-in", "prestart", spec.Staging.StageInManifest, "stage-in.txt", spec.Staging)
		appendStageTask(groupBody, "stage-out", "poststop", spec.Staging.StageOutManifest, "stage-out.txt", spec.Staging)
	}

	mainBody := groupBody.AppendNewBlock("task", []string{"main"}).Body()
	mainBody.SetAttributeValue("driver", cty.StringVal(spec.Driver))

	cfgBody := mainBody.AppendNewBlock("config", nil).Body()
	appendTaskConfig(cfgBody, spec, scriptName, scriptContent)

	if spec.Driver != "slurm" {
		tmplBody := mainBody.AppendNewBlock("template", nil).Body()
		tmplBody.SetAttributeValue("data", cty.StringVal(scriptContent))
		tmplBody.SetAttributeValue("destination", cty.StringVal(
			filepath.ToSlash(filepath.Join("local", scriptName))))
		tmplBody.SetAttributeValue("perms", cty.StringVal("0755"))
	}

	for _, t := range spec.ExtraTemplates {
		if spec.Driver == "slurm" {
			continue
		}
		tmplBody := mainBody.AppendNewBlock("template", nil).Body()
		tmplBody.SetAttributeValue("data", cty.StringVal(t.Data))
		tmplBody.SetAttributeValue("destination", cty.StringVal(t.Destination))
		if t.Perms != "" {
			tmplBody.SetAttributeValue("perms", cty.StringVal(t.Perms))
		}
	}

	if spec.Cores > 0 || spec.MemoryMB > 0 || spec.GPUs > 0 {
		resBody := mainBody.AppendNewBlock("resources", nil).Body()
		if spec.Cores > 0 {
			resBody.SetAttributeValue("cores", cty.NumberIntVal(int64(spec.Cores)))
		}
		if spec.MemoryMB > 0 {
			resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))
		}
		if spec.GPUs > 0 {
			resBody.AppendNewBlock("device", []string{"nvidia/gpu"}).Body().
				SetAttributeValue("count", cty.NumberIntVal(int64(spec.GPUs)))
		}
	}

	for _, art := range spec.Artifacts {
		artBody := mainBody.AppendNewBlock("artifact", nil).Body()
		// Use raw-string emission so `${attr.kernel.name}` etc. survive into
		// Nomad's interpolation pass (cty.StringVal would escape it to `$${`).
		setAttributeRawString(artBody, "source", art.Source)
		dst := art.Destination
		if dst == "" {
			dst = "local/"
		}
		artBody.SetAttributeValue("destination", cty.StringVal(dst))
		if art.Mode != "" {
			artBody.SetAttributeValue("mode", cty.StringVal(art.Mode))
		}
	}

	appendEnvBlock(mainBody, spec)

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// taskScriptShell returns the interpreter used to run the templated local script.
//
// Behaviour-alignment rule (2026-05-22): every OCI/container driver
// (docker, containerd-driver, podman, singularity) returns "/bin/sh"
// by default because (a) minimal images like Alpine ship sh but not
// bash, and (b) we want jobs to be portable across drivers — picking
// the lowest-common-denominator shell makes "swap docker → containerd"
// invisible to the user. Container-image authors who rely on bash
// semantics opt in explicitly via the `--shell=bash` directive, which
// is honoured by `pickTaskScriptShell` below.
//
// Non-OCI drivers (exec, exec2, raw_exec, java, hpc-bridge, slurm,
// pbs) run on the Nomad host filesystem where bash is reliably
// present, so they keep /bin/bash as the default.
func taskScriptShell(driver string) string {
	switch driver {
	case "containerd-driver", "docker", "podman", "singularity":
		return "/bin/sh"
	default:
		return "/bin/bash"
	}
}

// pickTaskScriptShell returns the shell to invoke the user's script,
// honouring an explicit Spec.Shell override when set, else falling back
// to the driver default per `taskScriptShell`.
//
// Spec.Shell accepts:
//   - "" → driver default.
//   - absolute path ("/bin/dash", "/usr/local/bin/fish") → used verbatim.
//   - bare name ("bash", "sh", "dash", "zsh", "fish", …) → resolved to
//     /bin/<name>. This is the common case; lets users write
//     `#ABC --shell=zsh` without spelling out a path that varies by
//     image (/bin/zsh vs /usr/bin/zsh vs /usr/local/bin/zsh).
//
// The shell binary's existence inside the target image is the user's
// responsibility — the kernel surfaces a clear ENOENT at exec time if
// the path doesn't resolve. We don't try to be clever about path
// probing because the script runs in a container whose filesystem we
// can't introspect at HCL-gen time.
func pickTaskScriptShell(spec Spec) string {
	v := strings.TrimSpace(spec.Shell)
	if v == "" {
		return taskScriptShell(spec.Driver)
	}
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "/bin/" + strings.ToLower(v)
}

// isOCIDriver reports whether the driver runs the task inside a
// container image (as opposed to a host-side sandbox or remote bridge).
// Used for OCI-specific config wiring (default hostname, image pull,
// etc.).
func isOCIDriver(driver string) bool {
	switch driver {
	case "docker", "containerd-driver", "podman", "singularity":
		return true
	}
	return false
}

// ociTaskScriptArg returns the script path passed to timeout/sh/bash for OCI-backed
// drivers. Relative "local/..." breaks when the image WORKDIR is not the allocation
// task directory (common with prebuilt scientific images); Nomad interpolates
// ${NOMAD_TASK_DIR} in args before the task starts.
//
// Use ${NOMAD_TASK_DIR}/<scriptName> (not .../local/<scriptName>): on some containerd
// stacks NOMAD_TASK_DIR inside the container already points at the mounted "local"
// area, so .../local/script.sh becomes /local/local/script.sh and fails.
func ociTaskScriptArg(driver, scriptArg string) string {
	switch driver {
	case "containerd-driver", "docker", "podman", "singularity":
		if strings.HasPrefix(scriptArg, "local/") {
			return "${NOMAD_TASK_DIR}/" + strings.TrimPrefix(scriptArg, "local/")
		}
	}
	return scriptArg
}

// ScriptArgForDriver returns the script path argument the Nomad task passes to
// sh/bash for this driver (equivalent to ociTaskScriptArg(local/<scriptName>)).
// Callers that re-exec the same script (e.g. Pixi wrappers) must use this path
// so docker/containerd tasks do not double-resolve "local/".
func ScriptArgForDriver(driver, scriptName string) string {
	scriptArg := filepath.ToSlash(filepath.Join("local", scriptName))
	return ociTaskScriptArg(driver, scriptArg)
}

func appendTaskConfig(cfgBody *hclwrite.Body, spec Spec, scriptName, scriptContent string) {
	scriptArg := filepath.ToSlash(filepath.Join("local", scriptName))
	scriptArg = ociTaskScriptArg(spec.Driver, scriptArg)
	sh := pickTaskScriptShell(spec)
	if spec.Driver == "slurm" || spec.Driver == "pbs" {
		inlineScript := escapeNomadInterpolation(scriptContent)
		cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("-c"),
			cty.StringVal(inlineScript),
		}))
		if spec.SlurmPartition != "" {
			cfgBody.SetAttributeValue("queue", cty.StringVal(spec.SlurmPartition))
		}
		if spec.SlurmAccount != "" {
			cfgBody.SetAttributeValue("account", cty.StringVal(spec.SlurmAccount))
		}
		workDir := spec.SlurmWorkDir
		if workDir == "" && spec.ChDir != "" {
			workDir = spec.ChDir
		}
		if workDir != "" {
			cfgBody.SetAttributeValue("work_dir", cty.StringVal(workDir))
		}
		stdoutFile := spec.SlurmStdoutFile
		if stdoutFile == "" && spec.OutputLog != "" {
			stdoutFile = spec.OutputLog
		}
		if stdoutFile != "" {
			cfgBody.SetAttributeValue("stdout_file", cty.StringVal(stdoutFile))
		}
		stderrFile := spec.SlurmStderrFile
		if stderrFile == "" && spec.ErrorLog != "" {
			stderrFile = spec.ErrorLog
		}
		if stderrFile != "" {
			cfgBody.SetAttributeValue("stderr_file", cty.StringVal(stderrFile))
		}
		if spec.SlurmNTasks > 0 {
			cfgBody.SetAttributeValue("ntasks", cty.NumberIntVal(int64(spec.SlurmNTasks)))
		}
		if spec.Cores > 0 {
			cfgBody.SetAttributeValue("cpus_per_task", cty.NumberIntVal(int64(spec.Cores)))
		}
		if spec.MemoryMB > 0 {
			cfgBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))
		}
		if spec.WalltimeSecs > 0 {
			cfgBody.SetAttributeValue("walltime", cty.StringVal(secondsToWalltime(spec.WalltimeSecs)))
		}
		if spec.SlurmReservation != "" {
			cfgBody.SetAttributeValue("reservation", cty.StringVal(spec.SlurmReservation))
		}
		if len(spec.SlurmExtraArgs) > 0 {
			vals := make([]cty.Value, len(spec.SlurmExtraArgs))
			for i, a := range spec.SlurmExtraArgs {
				vals[i] = cty.StringVal(a)
			}
			cfgBody.SetAttributeValue("extra_args", cty.ListVal(vals))
		}
	} else if spec.Driver == "singularity" {
		// Singularity/Apptainer's Nomad-driver config schema is intentionally
		// different from docker/containerd/podman: `command` is the apptainer
		// subcommand (run|exec), and `args` is what gets passed *inside* the
		// container.
		//
		// IMPORTANT: apptainer does NOT bind-mount ${NOMAD_TASK_DIR} by
		// default — the standard apptainer auto-binds (HOME, /tmp, /proc,
		// /sys, /dev, CWD) don't cover the Nomad alloc task directory, so
		// `<shell> ${NOMAD_TASK_DIR}/script.sh` fails inside the container
		// with "can't open /local/<script>: No such file or directory".
		//
		// Fix: inline the script body into the command-line via `-c`. The
		// shell receives the script verbatim. This avoids any host-path /
		// bind-mount dependency, at the cost of an ARG_MAX ceiling
		// (~128 KiB on Linux; abc job run scripts are typically a few KiB).
		// If/when scripts approach that ceiling, switch to writing the
		// script to /tmp inside the container (auto-bound by apptainer)
		// or use the driver's `binds` config to mount the task dir.
		//
		// The resulting shape is:
		//   command = "exec"
		//   args    = [<shell>, "-c", "<scriptContent>"]           (no walltime)
		//   args    = [<shell>, "-c", "timeout N <shell> -c '…'"]  (walltime)
		cfgBody.SetAttributeValue("command", cty.StringVal("exec"))
		// `scriptContent` already carries the user's script verbatim.
		// We pass it as a single `-c` string. Nomad-interpolation inside
		// the body is escaped via `escapeNomadInterpolation` so user
		// `$VAR` doesn't get eaten by Nomad's renderer.
		inline := escapeNomadInterpolation(scriptContent)
		// args entries are passed as separate argv tokens to the container
		// entrypoint. With command="exec" the apptainer command-line
		// becomes:  apptainer exec <image> arg0 arg1 arg2 ...
		// So walltime is implemented by prepending `timeout <n>` to argv
		// rather than wrapping the shell-c string (which would need
		// non-trivial quoting). `timeout` is in every standard Linux base
		// image (coreutils on debian/ubuntu, busybox on alpine).
		var argv []cty.Value
		if spec.WalltimeSecs > 0 {
			argv = append(argv,
				cty.StringVal("timeout"),
				cty.StringVal(fmt.Sprintf("%d", spec.WalltimeSecs)),
			)
		}
		argv = append(argv,
			cty.StringVal(sh),
			cty.StringVal("-c"),
			cty.StringVal(inline),
		)
		cfgBody.SetAttributeValue("args", cty.ListVal(argv))
	} else if spec.OutputLog != "" || spec.ErrorLog != "" {
		cmd := fmt.Sprintf("%s %s", sh, scriptArg)
		if spec.WalltimeSecs > 0 {
			cmd = fmt.Sprintf("timeout %d %s", spec.WalltimeSecs, cmd)
		}
		if spec.OutputLog != "" {
			cmd = fmt.Sprintf(`%s 1> >(tee -a "${NOMAD_TASK_DIR}/%s")`, cmd, spec.OutputLog)
		}
		if spec.ErrorLog != "" {
			cmd = fmt.Sprintf(`%s 2> >(tee -a "${NOMAD_TASK_DIR}/%s" >&2)`, cmd, spec.ErrorLog)
		}
		cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("-lc"), cty.StringVal(cmd),
		}))
	} else if spec.WalltimeSecs > 0 {
		cfgBody.SetAttributeValue("command", cty.StringVal("timeout"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal(fmt.Sprintf("%d", spec.WalltimeSecs)),
			cty.StringVal(sh),
			cty.StringVal(scriptArg),
		}))
	} else {
		cfgBody.SetAttributeValue("command", cty.StringVal(sh))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal(scriptArg),
		}))
	}

	// Default in-container hostname for OCI drivers, aligned across docker /
	// containerd-driver / podman / singularity so the same job emits the
	// same `hostname` to user code regardless of driver. Without this, each
	// driver picks its own default — docker uses the short container ID
	// (e.g. "87c47a24f284"), containerd leaves the Nomad cgroup-scope UUID
	// (e.g. "0cf4b62f-...main.scope"), podman uses the container short ID,
	// singularity leaves the host hostname. Aligning to ${NOMAD_SHORT_ALLOC_ID}
	// gives a stable 8-char identifier that matches Nomad's own UI.
	// Skipped if the user has explicitly set `--driver.config.hostname=…`.
	if isOCIDriver(spec.Driver) {
		if _, userSet := spec.DriverConfig["hostname"]; !userSet {
			setAttributeRawString(cfgBody, "hostname", "${NOMAD_SHORT_ALLOC_ID}")
		}
	}

	for _, k := range utils.SortedKeys(spec.DriverConfig) {
		// `command` and `args` are managed by the script-wrap path above —
		// they invoke the user's submitted script INSIDE the container so
		// `abc job run` keeps its HPC-script-like UX (write a shell script,
		// it runs against the container's filesystem). Driver-config
		// `command`/`args` would silently shadow the script and produce
		// confusing failures (image entrypoint runs the wrong thing, args
		// list looks like script args but isn't). Reserve `--driver.config`
		// for *container-shaped* settings (image, volumes, mounts, ports,
		// network_mode, ulimits, …); ignore command/args here.
		if k == "command" || k == "args" {
			continue
		}
		v := strings.TrimSpace(spec.DriverConfig[k])
		// JSON-array form: `key=["a","b","c"]` → HCL list(string).
		// Used for docker/containerd `volumes`, `mount`, `dns_servers`, etc.
		if items, ok := parseDriverConfigList(v); ok {
			if len(items) == 0 {
				cfgBody.SetAttributeValue(k, cty.ListValEmpty(cty.String))
				continue
			}
			vals := make([]cty.Value, len(items))
			for i, p := range items {
				vals[i] = cty.StringVal(p)
			}
			cfgBody.SetAttributeValue(k, cty.ListVal(vals))
			continue
		}
		if k == "extra_args" {
			// hpc-bridge/slurm convenience: whitespace-split into list(string).
			parts := strings.Fields(v)
			if len(parts) == 0 {
				parts = []string{v}
			}
			vals := make([]cty.Value, len(parts))
			for i, p := range parts {
				vals[i] = cty.StringVal(p)
			}
			cfgBody.SetAttributeValue(k, cty.ListVal(vals))
			continue
		}
		cfgBody.SetAttributeValue(k, cty.StringVal(v))
	}
	if spec.Driver != "slurm" && spec.ChDir != "" {
		cfgBody.SetAttributeValue("work_dir", cty.StringVal(spec.ChDir))
	}
}

func secondsToWalltime(seconds int) string {
	if seconds <= 0 {
		return "00:00:00"
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

func escapeNomadInterpolation(s string) string {
	s = strings.ReplaceAll(s, "${", "$${")
	s = strings.ReplaceAll(s, "%{", "%%{")
	return s
}

func appendEnvBlock(taskBody *hclwrite.Body, spec Spec) {
	if !spec.IncludeHPCCompatEnv && !hasExplicitRuntimeExposure(spec) && len(spec.Ports) == 0 && len(spec.StaticEnv) == 0 {
		return
	}
	envBody := taskBody.AppendNewBlock("env", nil).Body()

	if spec.TaskTmp {
		tmp := "${NOMAD_TASK_DIR}/tmp"
		envBody.SetAttributeValue("TMPDIR", cty.StringVal(tmp))
		envBody.SetAttributeValue("TMP", cty.StringVal(tmp))
		envBody.SetAttributeValue("TEMP", cty.StringVal(tmp))
	}

	if spec.IncludeHPCCompatEnv {
		envBody.SetAttributeValue("SLURM_JOB_ID", cty.StringVal("${NOMAD_ALLOC_ID}"))
		envBody.SetAttributeValue("PBS_JOBID", cty.StringVal("${NOMAD_ALLOC_ID}"))
		envBody.SetAttributeValue("SLURM_JOB_NAME", cty.StringVal("${NOMAD_JOB_NAME}"))
		envBody.SetAttributeValue("PBS_JOBNAME", cty.StringVal("${NOMAD_JOB_NAME}"))
		envBody.SetAttributeValue("SLURM_SUBMIT_DIR", cty.StringVal("${NOMAD_TASK_DIR}"))
		envBody.SetAttributeValue("PBS_O_WORKDIR", cty.StringVal("${NOMAD_TASK_DIR}"))
		envBody.SetAttributeValue("SLURM_ARRAY_TASK_ID", cty.StringVal("${NOMAD_ALLOC_INDEX}"))
		envBody.SetAttributeValue("PBS_ARRAYID", cty.StringVal("${NOMAD_ALLOC_INDEX}"))
		envBody.SetAttributeValue("SLURM_NTASKS", cty.StringVal("${NOMAD_GROUP_COUNT}"))
		envBody.SetAttributeValue("PBS_NP", cty.StringVal("${NOMAD_GROUP_COUNT}"))
		envBody.SetAttributeValue("SLURMD_NODENAME", cty.StringVal("${NOMAD_ALLOC_HOST}"))
		envBody.SetAttributeValue("PBS_O_HOST", cty.StringVal("${NOMAD_ALLOC_HOST}"))
		envBody.SetAttributeValue("SLURM_CPUS_ON_NODE", cty.StringVal("${NOMAD_CPU_CORES}"))
		envBody.SetAttributeValue("PBS_NUM_PPN", cty.StringVal("${NOMAD_CPU_CORES}"))
		envBody.SetAttributeValue("SLURM_MEM_PER_NODE", cty.StringVal("${NOMAD_MEMORY_LIMIT}"))
		envBody.SetAttributeValue("PBS_MEM", cty.StringVal("${NOMAD_MEMORY_LIMIT}"))
	}

	type runtimeVar struct {
		flag bool
		env  string
	}
	for _, e := range []runtimeVar{
		{spec.ExposeAllocID, "NOMAD_ALLOC_ID"},
		{spec.ExposeShortAllocID, "NOMAD_SHORT_ALLOC_ID"},
		{spec.ExposeAllocName, "NOMAD_ALLOC_NAME"},
		{spec.ExposeAllocIndex, "NOMAD_ALLOC_INDEX"},
		{spec.ExposeJobID, "NOMAD_JOB_ID"},
		{spec.ExposeJobName, "NOMAD_JOB_NAME"},
		{spec.ExposeParentJobID, "NOMAD_JOB_PARENT_ID"},
		{spec.ExposeGroupName, "NOMAD_GROUP_NAME"},
		{spec.ExposeTaskName, "NOMAD_TASK_NAME"},
		{spec.ExposeNamespaceEnv, "NOMAD_NAMESPACE"},
		{spec.ExposeDCEnv, "NOMAD_DC"},
		{spec.ExposeCPULimit, "NOMAD_CPU_LIMIT"},
		{spec.ExposeCPUCores, "NOMAD_CPU_CORES"},
		{spec.ExposeMemLimit, "NOMAD_MEMORY_LIMIT"},
		{spec.ExposeMemMaxLimit, "NOMAD_MEMORY_MAX_LIMIT"},
		{spec.ExposeAllocDir, "NOMAD_ALLOC_DIR"},
		{spec.ExposeTaskDir, "NOMAD_TASK_DIR"},
		{spec.ExposeSecretsDir, "NOMAD_SECRETS_DIR"},
	} {
		if e.flag {
			envBody.SetAttributeValue(e.env, cty.StringVal(fmt.Sprintf("${%s}", e.env)))
		}
	}

	for _, p := range spec.Ports {
		up := strings.ToUpper(p)
		envBody.SetAttributeValue("NOMAD_IP_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_IP_%s}", p)))
		envBody.SetAttributeValue("NOMAD_PORT_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_PORT_%s}", p)))
		envBody.SetAttributeValue("NOMAD_ADDR_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_ADDR_%s}", p)))
	}

	for _, k := range utils.SortedKeys(spec.StaticEnv) {
		envBody.SetAttributeValue(k, cty.StringVal(spec.StaticEnv[k]))
	}
}

func hasExplicitRuntimeExposure(spec Spec) bool {
	return spec.TaskTmp ||
		spec.ExposeAllocID ||
		spec.ExposeShortAllocID ||
		spec.ExposeAllocName ||
		spec.ExposeAllocIndex ||
		spec.ExposeJobID ||
		spec.ExposeJobName ||
		spec.ExposeParentJobID ||
		spec.ExposeGroupName ||
		spec.ExposeTaskName ||
		spec.ExposeNamespaceEnv ||
		spec.ExposeDCEnv ||
		spec.ExposeCPULimit ||
		spec.ExposeCPUCores ||
		spec.ExposeMemLimit ||
		spec.ExposeMemMaxLimit ||
		spec.ExposeAllocDir ||
		spec.ExposeTaskDir ||
		spec.ExposeSecretsDir
}

// appendWavePrestartTask emits the "wave-build" prestart task that calls the
// Wave CLI with --await to block until the container image is pullable.
//
// Structure:
//   - driver: exec (runs on the host, not inside a container)
//   - template (secrets/wave.env): injects TOWER_ACCESS_TOKEN from a Nomad Variable
//   - template (local/environment.yml): the conda env file embedded at submit time
//   - template (local/wave-build.sh): downloads the wave binary via curl (using
//     uname for arch detection, matching the micromamba pattern) then calls wave --await
//   - resources: minimal (200 MHz CPU, 256 MB memory)
//
// Note: the wave binary is downloaded at runtime via curl rather than via a Nomad
// artifact stanza because Nomad artifact stanzas do not resolve ${attr.*} node
// attributes in source URLs — they are passed literally to go-getter, which then
// fails. The curl approach mirrors how micromamba fetches its binary.
// appendStageTask emits an s5cmd staging task (spec abc-job-data-staging Part A).
// hook is "prestart" (stage-in, before main) or "poststop" (stage-out, after main
// succeeds). The manifest is written as a template at local/<manifestFile> and run
// via `s5cmd run`. The nf-work host volume (carrying the s5cmd binary) is mounted
// read-only. With sidecar=false a non-zero prestart exit aborts the job before main;
// poststop runs only on main success (so partial outputs are not staged out).
func appendStageTask(groupBody *hclwrite.Body, name, hook, manifest, manifestFile string, s StagingSpec) {
	taskBody := groupBody.AppendNewBlock("task", []string{name}).Body()

	lc := taskBody.AppendNewBlock("lifecycle", nil).Body()
	lc.SetAttributeValue("hook", cty.StringVal(hook))
	lc.SetAttributeValue("sidecar", cty.BoolVal(false))

	taskBody.SetAttributeValue("driver", cty.StringVal("exec"))

	// Mount the host volume carrying the s5cmd binary (read-only).
	vm := taskBody.AppendNewBlock("volume_mount", nil).Body()
	vm.SetAttributeValue("volume", cty.StringVal(s.HostVolumeName))
	vm.SetAttributeValue("destination", cty.StringVal(s.HostVolumeMount))
	vm.SetAttributeValue("read_only", cty.BoolVal(true))

	// s5cmd credentials / endpoint (MinIO key+secret, S3 endpoint, private-CA
	// handling) injected as env from the supplied map. Deterministic ordering.
	if len(s.Env) > 0 {
		var b strings.Builder
		for _, k := range utils.SortedKeys(s.Env) {
			fmt.Fprintf(&b, "%s=%s\n", k, s.Env[k])
		}
		envTmpl := taskBody.AppendNewBlock("template", nil).Body()
		envTmpl.SetAttributeValue("data", cty.StringVal(b.String()))
		envTmpl.SetAttributeValue("destination", cty.StringVal("secrets/s5cmd.env"))
		envTmpl.SetAttributeValue("env", cty.BoolVal(true))
	}

	// The declarative s5cmd commands-file (generated by internal/jobstage).
	mTmpl := taskBody.AppendNewBlock("template", nil).Body()
	mTmpl.SetAttributeValue("data", cty.StringVal(manifest))
	mTmpl.SetAttributeValue("destination", cty.StringVal("local/"+manifestFile))
	mTmpl.SetAttributeValue("perms", cty.StringVal("0644"))
	mTmpl.SetAttributeValue("change_mode", cty.StringVal("noop"))

	cfgBody := taskBody.AppendNewBlock("config", nil).Body()
	// Wrap s5cmd in a shell that creates + cd's into the alloc-shared DestRoot
	// (shell-expands $NOMAD_ALLOC_DIR at runtime; the manifest's local paths are
	// relative to it) then runs the manifest from the task dir. Bare $NOMAD_* (no
	// braces) avoids hclwrite's ${...} escaping and is expanded by /bin/sh.
	cfgBody.SetAttributeValue("command", cty.StringVal("/bin/sh"))
	// --no-verify-ssl is a global s5cmd flag (must precede the `run` subcommand);
	// set on private-CA deployments (abc-seedling) where the stage task container
	// does not trust the MinIO certificate.
	s5cmdGlobal := ""
	if s.SkipTLS {
		s5cmdGlobal = "--no-verify-ssl "
	}
	shCmd := fmt.Sprintf(`mkdir -p "%s" && cd "%s" && %s %srun "$NOMAD_TASK_DIR/local/%s"`,
		s.DestRoot, s.DestRoot, s.S5cmdPath, s5cmdGlobal, manifestFile)
	cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
		cty.StringVal("-c"),
		cty.StringVal(shCmd),
	}))

	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(500))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(512))
}

func appendWavePrestartTask(groupBody *hclwrite.Body, w WaveSpec) {
	taskBody := groupBody.AppendNewBlock("task", []string{"wave-build"}).Body()

	lc := taskBody.AppendNewBlock("lifecycle", nil).Body()
	lc.SetAttributeValue("hook", cty.StringVal("prestart"))
	lc.SetAttributeValue("sidecar", cty.BoolVal(false))

	taskBody.SetAttributeValue("driver", cty.StringVal("exec"))

	// TOWER_ACCESS_TOKEN injected from a Nomad Variable, but only when the key
	// holds a non-empty value. The inner {{ if }} guard prevents an empty string
	// from being exported as an env var, which would cause the Wave API to return
	// 401 Unauthorized (it treats any TOWER_ACCESS_TOKEN value as an auth attempt).
	tokenData := fmt.Sprintf(
		"{{ with nomadVar %q }}{{ if .%s }}TOWER_ACCESS_TOKEN={{ .%s }}{{ end }}{{ end }}\n",
		w.TokenSecretPath, w.TokenSecretKey, w.TokenSecretKey,
	)
	tokenTmpl := taskBody.AppendNewBlock("template", nil).Body()
	tokenTmpl.SetAttributeValue("data", cty.StringVal(tokenData))
	tokenTmpl.SetAttributeValue("destination", cty.StringVal("secrets/wave.env"))
	tokenTmpl.SetAttributeValue("env", cty.BoolVal(true))

	// Embedded conda environment.yml.
	if w.CondaFileContent != "" {
		envTmpl := taskBody.AppendNewBlock("template", nil).Body()
		envTmpl.SetAttributeValue("data", cty.StringVal(w.CondaFileContent))
		envTmpl.SetAttributeValue("destination", cty.StringVal("local/environment.yml"))
		envTmpl.SetAttributeValue("perms", cty.StringVal("0644"))
		envTmpl.SetAttributeValue("change_mode", cty.StringVal("noop"))
	}

	// wave-build.sh: downloads wave binary at runtime (arch via uname, matching
	// the micromamba pattern), then calls wave --await to block until the container
	// image is pullable.
	buildScript := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
_ABC_KERNEL="$(uname -s | tr '[:upper:]' '[:lower:]')"
_ABC_ARCH="$(uname -m)"
case "${_ABC_ARCH}" in x86_64) _ABC_ARCH=amd64 ;; aarch64) _ABC_ARCH=arm64 ;; esac
curl -fsSL "%s-${_ABC_KERNEL}-${_ABC_ARCH}" -o "${NOMAD_TASK_DIR}/wave" || {
  echo "[abc] wave: binary download failed" >&2; exit 1
}
chmod +x "${NOMAD_TASK_DIR}/wave"
echo "[abc] wave-exec: waiting for Wave container build (platform: %s)..."
"${NOMAD_TASK_DIR}/wave" \
  --conda-file "${NOMAD_TASK_DIR}/environment.yml" \
  --platform %s \
  --await
echo "[abc] wave-exec: container image ready"
`, w.BinarySourceURL, w.Platform, w.Platform)

	scriptTmpl := taskBody.AppendNewBlock("template", nil).Body()
	scriptTmpl.SetAttributeValue("data", cty.StringVal(buildScript))
	scriptTmpl.SetAttributeValue("destination", cty.StringVal("local/wave-build.sh"))
	scriptTmpl.SetAttributeValue("perms", cty.StringVal("0755"))

	cfgBody := taskBody.AppendNewBlock("config", nil).Body()
	cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
	cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
		cty.StringVal("local/wave-build.sh"),
	}))

	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(200))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(256))
}
