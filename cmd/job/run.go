// Package job implements the "abc job" command group, including "abc job run"
// which parses preamble directives and generates a Nomad HCL batch job spec.
package job

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/abc-cluster/abc-cluster-cli/internal/jurist"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <script>",
		Short: "Generate and submit a Nomad HCL batch job from an annotated script",
		Long: `Parse #ABC/#NOMAD preamble directives from a script and produce a Nomad
HCL job spec. By default it is registered directly with Nomad.
Use --no-submit to print HCL without submission.

DIRECTIVE PRECEDENCE (highest → lowest)
  CLI flags  >  #ABC preamble  >  #NOMAD preamble  >  NOMAD_* env vars  >  params file

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 1 — SCHEDULER  (configure HCL stanza fields)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --name=<string>              Job name (default: script filename stem)
  --region=<string>            Nomad region
  --namespace=<string>         Nomad namespace
  --dc=<string>                Target datacenter (repeatable)
  --priority=<1-100>           Scheduler priority (default: 50)
  --nodes=<int>                Parallel group instances / array width (default: 1)
  --cores=<int>                CPU cores per task
  --mem=<size>[K|M|G]          Memory per task (e.g. 4G, 512M)
  --gpus=<int>                 GPU count (nvidia/gpu device plugin)
  --time=<HH:MM:SS>            Walltime limit; wraps command in timeout(1)
  --chdir=<path>               Working directory inside task sandbox
  --driver=<string>            Task driver: exec (default), raw_exec, hpc-bridge, docker, containerd (→ containerd-driver)
  --depend=<complete:job-id>   Block on another job via prestart lifecycle hook
  --output=<filename>          Tee stdout to $NOMAD_TASK_DIR/<filename>
  --error=<filename>           Tee stderr to $NOMAD_TASK_DIR/<filename>
  --constraint=<attr><op><val> Nomad placement constraint (repeatable)
                               Ops: == != =~ !~ < <= > >=
                               Example: --constraint=region==za-cpt
  --affinity=<expr>[,weight=N] Nomad placement affinity (repeatable)
                               Example: --affinity=datacenter==c1,weight=75
  --no-network                 Disable network access (Nomad mode = "none")
  --port=<label>               Dynamic port; injects NOMAD_IP/PORT/ADDR_<label>
  --driver.config.<key>=<val>  Pass *container-shaped* driver config (image,
                               volumes, mounts, network_mode, ports, ulimits,
                               privileged, …). Two forms:
                                 Scalars: --driver.config.image=alpine:3.19
                                 Lists  : --driver.config.volumes=["host:/c"]
                                          (JSON-array; no spaces inside the value)
                               NOT allowed: command, args. The submitted script
                               body is what runs inside the container — abc wraps
                               it so your shell script gets the HPC-script-like
                               UX (driver-default-bash invokes script_path). Put
                               command-line arguments INSIDE the script body.

SOFTWARE STACK  (orthogonal to --driver; see USAGE.md job run / Software stack)
  --runtime=<kind>             Stack provisioner: pixi-exec (alias: pixi)
  --from=<path-or-uri>       Backend-native definition (pixi-exec: path to pixi.toml)

TASK WORKSPACE
  --task-tmp                   Use ${NOMAD_TASK_DIR}/tmp for TMPDIR/TMP/TEMP (mkdir at start)

RESCHEDULE  (stored as abc_reschedule_* meta keys)
  --reschedule-mode=<delay|fail>
  --reschedule-attempts=<int>
  --reschedule-interval=<duration>   e.g. 30s
  --reschedule-delay=<duration>      e.g. 5s
  --reschedule-max-delay=<duration>  e.g. 1m

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 2 — RUNTIME EXPOSURE  (inject NOMAD_* vars into task env block)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Note: NOMAD_REGION is always injected by Nomad automatically.
PBS_* and SLURM_* compatibility aliases are always emitted.

  Task identity
    --alloc_id         NOMAD_ALLOC_ID        (unique per execution)
    --short_alloc_id   NOMAD_SHORT_ALLOC_ID
    --alloc_name       NOMAD_ALLOC_NAME      (<job>.<group>[<index>])
    --alloc_index      NOMAD_ALLOC_INDEX     (0-based; use to shard array jobs)
    --job_id           NOMAD_JOB_ID
    --job_name         NOMAD_JOB_NAME
    --parent_job_id    NOMAD_JOB_PARENT_ID   (dispatched jobs only)
    --group_name       NOMAD_GROUP_NAME
    --task_name        NOMAD_TASK_NAME
    --namespace        NOMAD_NAMESPACE       (env exposure; --namespace=<n> for placement)
    --dc               NOMAD_DC              (env exposure; --dc=<n> for placement)

  Resources
    --cpu_limit        NOMAD_CPU_LIMIT       (MHz)
    --cpu_cores        NOMAD_CPU_CORES       (use for -t in BWA/samtools/STAR)
    --mem_limit        NOMAD_MEMORY_LIMIT    (MB; use for JVM -Xmx)
    --mem_max_limit    NOMAD_MEMORY_MAX_LIMIT

  Directories
    --alloc_dir        NOMAD_ALLOC_DIR       (shared across the group)
    --task_dir         NOMAD_TASK_DIR        (per-task private scratch)
    --secrets_dir      NOMAD_SECRETS_DIR     (in-memory, noexec)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 3 — META  (Nomad meta block → NOMAD_META_<KEY> in the task)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --meta=<key>=<value>         Repeatable. Key is uppercased for env access.
                               Example: --meta=sample_id=S001
                                        → NOMAD_META_SAMPLE_ID=S001

ABC-NODES ENHANCED FLOOR  (automatic env injection)
  For contexts with cluster_type abc-nodes, when capabilities (or synced
  admin.services URLs) indicate Loki / Prometheus / Grafana Alloy, generated
  batch tasks include ABC_NODES_CLUSTER_FLOOR=enhanced plus ABC_NODES_LOKI_*,
  ABC_NODES_PROMETHEUS_*, and ABC_NODES_GRAFANA_ALLOY_HTTP as appropriate.
  Base abc-nodes clusters (no monitoring stack) omit these variables.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PARAMS FILE  (YAML, lowest priority after env vars)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --params-file=<path>         YAML file with directive key/value pairs.
                               Nested keys are dot-flattened:
                                 cores: 8  →  --cores=8
                                 meta:
                                   sample: S001  →  --meta=sample=S001

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
INLINE COMMENTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Trailing shell comments are stripped from #ABC/#NOMAD lines, so
  annotated preambles are valid:
    #ABC --cores=8    # 8 cores per task (same as SLURM --cpus-per-task)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EXAMPLES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  # Built-in verification workload — randomised stress-ng across CPU / VM / I/O
  abc job run hello-abc

  # Inject a debug sleep so you can exec into the alloc before work begins
  abc job run hello-abc --sleep=120s
  abc job run myscript.sh  --sleep=5m

  # Preview generated HCL without submitting
  abc job run bwa-align.sh --no-submit

  # Pipe generated HCL to nomad directly
  abc job run bwa-align.sh --no-submit | nomad job run -

  # Submit (default) and tail logs immediately
  abc job run bwa-align.sh --region za-cpt --watch

  # Dry-run: plan server-side, show placement feasibility
  abc job run bwa-align.sh --dry-run --region za-cpt

  # Submit with preamble directive overridden from CLI
  abc job run bwa-align.sh --nodes=96 --cores=16

  # Write HCL to file without submitting
  abc job run bwa-align.sh --output-file bwa-align.hcl`,
		Args: cobra.ExactArgs(1),
		RunE: runJob,
	}

	// Submission modes
	cmd.Flags().Bool("submit", true, "Submit job to Nomad (default true)")
	cmd.Flags().Bool("no-submit", false, "Generate HCL only (do not submit)")
	cmd.Flags().Bool("dry-run", false, "Plan job server-side without submitting")
	cmd.Flags().Bool("watch", false, "Stream logs after submission")
	cmd.Flags().Duration("watch-timeout", 0, "Timeout for --watch log streaming (0 = no timeout)")
	cmd.Flags().Bool("notify", false, "Print ntfy subscription URL after submit (requires capabilities.notifications)")
	cmd.Flags().String("output-file", "", "Write generated HCL to file instead of stdout")

	// Scheduler overrides (mirror preamble Class 1)
	cmd.Flags().String("name", "", "Job name")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().String("region", "", "Nomad region")
	cmd.Flags().StringSlice("dc", nil, "Target datacenter(s)")
	cmd.Flags().Int("priority", 0, "Scheduler priority (1-100)")
	cmd.Flags().Int("nodes", 0, "Number of group instances")
	cmd.Flags().Int("cores", 0, "CPU cores per task")
	cmd.Flags().String("mem", "", "Memory per task (e.g. 8G, 512M)")
	cmd.Flags().Int("gpus", 0, "GPU count")
	cmd.Flags().String("time", "", "Walltime limit HH:MM:SS")
	cmd.Flags().String("chdir", "", "Working directory inside task sandbox")
	cmd.Flags().String("depend", "", "Dependency spec (complete:<job-id>)")
	cmd.Flags().String("driver", "", "Task driver (exec, raw_exec, hpc-bridge, docker, containerd aliases to containerd-driver)")
	cmd.Flags().String("output", "", "Tee stdout to $NOMAD_TASK_DIR/<filename>")
	cmd.Flags().String("error", "", "Tee stderr to $NOMAD_TASK_DIR/<filename>")
	cmd.Flags().String("conda", "", "Conda spec string or path to env YAML (abc meta key: abc_conda)")
	cmd.Flags().String("runtime", "", "Software stack runtime: pixi-exec (alias pixi); see USAGE.md")
	cmd.Flags().String("from", "", "Definition path/URI for --runtime (e.g. pixi.toml on the execution host)")
	cmd.Flags().Bool("task-tmp", false, "Use ${NOMAD_TASK_DIR}/tmp for TMPDIR/TMP/TEMP (see USAGE.md)")
	cmd.Flags().Bool("no-network", false, "Disable network access for this job")
	cmd.Flags().StringSlice("port", nil, "Named network ports")

	// Reschedule
	cmd.Flags().String("reschedule-mode", "", "Reschedule mode (delay, fail)")
	cmd.Flags().Int("reschedule-attempts", 0, "Max reschedule attempts")
	cmd.Flags().String("reschedule-interval", "", "Reschedule interval (e.g. 30s)")
	cmd.Flags().String("reschedule-delay", "", "Reschedule base delay (e.g. 5s)")
	cmd.Flags().String("reschedule-max-delay", "", "Reschedule max delay (e.g. 1m)")

	// Meta + params
	cmd.Flags().StringToString("meta", nil, "Meta key=value (repeatable)")
	cmd.Flags().StringToString("driver.config", nil, "Driver config key=value (repeatable)")
	cmd.Flags().String("params-file", "", "YAML params file path")

	// Placement
	cmd.Flags().StringArray("constraint", nil,
		"Nomad placement constraint (repeatable); e.g. --constraint='${node.class}==slurm-login'")
	cmd.Flags().StringArray("affinity", nil,
		"Nomad placement affinity (repeatable); e.g. --affinity='datacenter==c1,weight=75'")
	cmd.Flags().Bool("spread", false,
		"Emit a Nomad spread stanza on ${node.unique.id} for 1-per-node distribution (best-effort)")

	// Script file handling
	cmd.Flags().Bool("chmod", true, "Mark the script file as executable before submission (chmod +x). Disable with --chmod=false.")

	// Artifact injection (used internally by abc data download for exec driver binaries)
	cmd.Flags().StringArray("artifact", nil, "Nomad artifact URL to fetch into the task directory before the task starts (repeatable); used by abc data download for exec driver binary staging")
	cmd.Flags().String("artifact-dest", "", "Destination path for the first --artifact entry (e.g. local/s5cmd to rename the downloaded file); used by abc data download")
	cmd.Flags().String("artifact-mode", "", "Nomad artifact mode for the first --artifact entry: file, dir, or any (default); use file to save to an exact path")

	// SLURM passthrough
	cmd.Flags().String("sleep", "",
		"Inject a sleep at the start of the job script for interactive debugging via exec\n"+
			"    (e.g. --sleep=60s, --sleep=5m, --sleep=120). The allocation stays alive\n"+
			"    and running so you can use 'nomad alloc exec' or the portal exec feature\n"+
			"    to log in and inspect the environment before the workload begins.")
	cmd.Flags().StringArray("slurm-extra", nil,
		"Extra sbatch argument(s) (repeatable); e.g. --slurm-extra='--qos=high'")
	cmd.Flags().String("reservation", "", "SLURM reservation name (maps to #SBATCH --reservation)")

	// Preamble mode + HPC compat
	cmd.Flags().String("preamble-mode", "auto",
		"Preamble parsing mode: auto (default), abc (ignore #SBATCH), slurm (require #SBATCH)")
	cmd.Flags().Bool("hpc-compat-env", false,
		"Inject SLURM_*/PBS_* environment aliases into the task env block")

	// SSH execution mode
	cmd.Flags().Bool("ssh", false, "Execute the job on a remote host via SSH instead of submitting to Nomad")
	cmd.Flags().Duration("ssh-timeout", 0, "Timeout for SSH job execution (e.g. 30m, 2h); 0 means no timeout")

	// Input format
	cmd.Flags().String("format", "", "Input format: shell (default, #ABC preamble script) or hcl (native Nomad HCL job definition). Auto-detected from file extension when omitted.")

	// HCL variable overrides (--format=hcl only)
	cmd.Flags().StringArray("var", nil, "HCL job variable override in key=value form (repeatable, --format=hcl only). Example: --var source=s3://my-bucket/data/")

	return cmd
}

func applyCLIFlags(cmd *cobra.Command, spec *jobSpec) error {
	if v, _ := cmd.Flags().GetString("name"); v != "" {
		spec.Name = v
	}
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		spec.Namespace = v
	}
	if v, _ := cmd.Flags().GetString("region"); v != "" {
		spec.Region = v
	}
	if v, _ := cmd.Flags().GetStringSlice("dc"); len(v) > 0 {
		spec.Datacenters = v
	}
	if v, _ := cmd.Flags().GetInt("priority"); v != 0 {
		spec.Priority = v
	}
	if v, _ := cmd.Flags().GetInt("nodes"); v != 0 {
		spec.Nodes = v
	}
	if v, _ := cmd.Flags().GetInt("cores"); v != 0 {
		spec.Cores = v
	}
	if v, _ := cmd.Flags().GetString("mem"); v != "" {
		mb, err := parseMemoryMB(v)
		if err != nil {
			return err
		}
		spec.MemoryMB = mb
	}
	if v, _ := cmd.Flags().GetInt("gpus"); v != 0 {
		spec.GPUs = v
	}
	if v, _ := cmd.Flags().GetString("time"); v != "" {
		secs, err := walltimeToSeconds(v)
		if err != nil {
			return err
		}
		spec.WalltimeSecs = secs
	}
	if v, _ := cmd.Flags().GetString("chdir"); v != "" {
		spec.ChDir = v
	}
	if v, _ := cmd.Flags().GetString("depend"); v != "" {
		spec.Depend = v
	}
	if v, _ := cmd.Flags().GetString("driver"); v != "" {
		spec.Driver = utils.NormalizeNomadTaskDriver(v)
	}
	if v, _ := cmd.Flags().GetString("reschedule-mode"); v != "" {
		spec.RescheduleMode = v
	}
	if v, _ := cmd.Flags().GetInt("reschedule-attempts"); v != 0 {
		spec.RescheduleAttempts = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-interval"); v != "" {
		spec.RescheduleInterval = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-delay"); v != "" {
		spec.RescheduleDelay = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-max-delay"); v != "" {
		spec.RescheduleMaxDelay = v
	}
	if v, _ := cmd.Flags().GetString("output"); v != "" {
		spec.OutputLog = v
	}
	if v, _ := cmd.Flags().GetString("error"); v != "" {
		spec.ErrorLog = v
	}
	if v, _ := cmd.Flags().GetString("conda"); v != "" {
		spec.Conda = v
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		spec.Meta["abc_conda"] = v
	}
	if m, _ := cmd.Flags().GetStringToString("meta"); len(m) > 0 {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		for k, v := range m {
			spec.Meta[k] = v
		}
	}
	if dc, _ := cmd.Flags().GetStringToString("driver.config"); len(dc) > 0 {
		// Reject command/args here for the same reason as the #ABC directive
		// path: they would shadow the submitted script.
		for k := range dc {
			if k == "command" || k == "args" {
				return fmt.Errorf(
					"--driver.config %s=… is not allowed: this would shadow the "+
						"submitted script. Reserve --driver.config for container-shaped "+
						"settings (image, volumes, mounts, network_mode, …); write the "+
						"actual command + arguments in the script body.",
					k,
				)
			}
		}
		if spec.DriverConfig == nil {
			spec.DriverConfig = map[string]string{}
		}
		for k, v := range dc {
			spec.DriverConfig[k] = v
		}
	}
	if ps, _ := cmd.Flags().GetStringSlice("port"); len(ps) > 0 {
		spec.Ports = ps
	}
	if v, _ := cmd.Flags().GetBool("no-network"); v {
		spec.NoNetwork = true
	}
	if v, _ := cmd.Flags().GetBool("hpc-compat-env"); v {
		spec.IncludeHPCCompatEnv = true
	}
	if v, _ := cmd.Flags().GetString("runtime"); v != "" {
		spec.Runtime = v
	}
	if v, _ := cmd.Flags().GetString("from"); v != "" {
		spec.From = v
	}
	if v, _ := cmd.Flags().GetBool("task-tmp"); v {
		spec.TaskTmp = true
	}
	if vs, _ := cmd.Flags().GetStringArray("constraint"); len(vs) > 0 {
		for _, expr := range vs {
			c, err := parseConstraint(expr)
			if err != nil {
				return fmt.Errorf("--constraint: %w", err)
			}
			spec.Constraints = append(spec.Constraints, c)
		}
	}
	if vs, _ := cmd.Flags().GetStringArray("affinity"); len(vs) > 0 {
		for _, expr := range vs {
			a, err := parseAffinity(expr)
			if err != nil {
				return fmt.Errorf("--affinity: %w", err)
			}
			spec.Affinities = append(spec.Affinities, a)
		}
	}
	if v, _ := cmd.Flags().GetBool("spread"); v {
		spec.Spread = true
	}
	if v, _ := cmd.Flags().GetString("sleep"); v != "" {
		secs, err := parseSleepDuration(v)
		if err != nil {
			return fmt.Errorf("--sleep: %w", err)
		}
		spec.DebugSleepSecs = secs
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		spec.Meta["abc_debug_sleep"] = fmt.Sprintf("%ds", secs)
	}
	if vs, _ := cmd.Flags().GetStringArray("slurm-extra"); len(vs) > 0 {
		spec.SlurmExtraArgs = append(spec.SlurmExtraArgs, vs...)
	}
	if v, _ := cmd.Flags().GetString("reservation"); v != "" {
		spec.SlurmReservation = v
	}
	if vs, _ := cmd.Flags().GetStringArray("artifact"); len(vs) > 0 {
		dest, _ := cmd.Flags().GetString("artifact-dest")
		mode, _ := cmd.Flags().GetString("artifact-mode")
		for i, raw := range vs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			// Support inline "url|dest|mode" per-artifact encoding produced by
			// buildDataDownloadJobRunArgs when multiple artifacts need different paths.
			u, inlineDest, inlineMode := parseArtifactFlagValue(raw)
			a := artifactSpec{Source: u}
			switch {
			case inlineDest != "" || inlineMode != "":
				a.Destination = inlineDest
				a.Mode = inlineMode
			case i == 0:
				if strings.TrimSpace(dest) != "" {
					a.Destination = strings.TrimSpace(dest)
				}
				if strings.TrimSpace(mode) != "" {
					a.Mode = strings.TrimSpace(mode)
				}
			}
			spec.Artifacts = append(spec.Artifacts, a)
		}
	}
	syncStackMetaKeys(spec)
	syncTaskTmpMeta(spec)
	return nil
}

// detectJobFormat returns "hcl" for files whose extension unambiguously identifies
// them as native Nomad HCL job definitions (.hcl, .nomad.hcl), and "shell" otherwise.
func detectJobFormat(path string) string {
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".nomad.hcl") || strings.ToLower(filepath.Ext(base)) == ".hcl" {
		return "hcl"
	}
	return "shell"
}

func runJob(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]

	// Resolve --format (shell | hcl); auto-detect from extension when omitted.
	format, _ := cmd.Flags().GetString("format")
	if format == "" {
		if scriptPath != "hello-abc" {
			format = detectJobFormat(scriptPath)
		} else {
			format = "shell"
		}
	}
	switch format {
	case "hcl":
		return runJobNativeHCL(cmd, scriptPath)
	case "shell":
		// fall through to existing path
	default:
		return fmt.Errorf("unknown --format %q: must be shell or hcl", format)
	}

	isBuiltInHelloAbc := scriptPath == "hello-abc"
	var (
		scriptBytes []byte
		scriptBase  string
		defaultName string
		err         error
	)
	if isBuiltInHelloAbc {
		scriptBase = helloAbcScriptBase
		defaultName = "hello-abc"
		scriptBytes = []byte(helloAbcScriptBody)
	} else {
		f, openErr := os.Open(scriptPath)
		if openErr != nil {
			return fmt.Errorf("cannot open script %q: %w", scriptPath, openErr)
		}
		defer f.Close()
		if doChmod, _ := cmd.Flags().GetBool("chmod"); doChmod {
			if stat, statErr := f.Stat(); statErr == nil && stat.Mode()&0111 == 0 {
				_ = os.Chmod(scriptPath, stat.Mode()|0111)
			}
		}
		scriptBytes, err = io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("cannot read script %q: %w", scriptPath, err)
		}
		scriptBase = filepath.Base(scriptPath)
		defaultName = strings.TrimSuffix(scriptBase, filepath.Ext(scriptBase))
	}

	abcDirs, nomadDirs, slurmDirs, pbsDirs, err := parsePreamble(bytes.NewReader(scriptBytes))
	if err != nil {
		return fmt.Errorf("failed to parse script preamble: %w", err)
	}

	mode := preambleModeAuto
	if modeStr, _ := cmd.Flags().GetString("preamble-mode"); modeStr != "" {
		switch modeStr {
		case "auto":
			mode = preambleModeAuto
		case "abc":
			mode = preambleModeABC
		case "slurm":
			mode = preambleModeSlurm
		case "pbs":
			mode = preambleModePBS
		default:
			return fmt.Errorf("unknown --preamble-mode %q: must be auto, abc, slurm, or pbs", modeStr)
		}
	}

	scriptSpec, useSBATCH, err := resolveSpecRaw(abcDirs, nomadDirs, slurmDirs, pbsDirs, mode)
	if err != nil {
		return err
	}

	// Merge in documented precedence: CLI > preamble > env > params.
	spec := &jobSpec{}
	if isBuiltInHelloAbc {
		spec = mergeSpec(spec, buildHelloAbcSpec())
	}

	if paramsFile, _ := cmd.Flags().GetString("params-file"); paramsFile != "" {
		params, err := loadParamsFile(paramsFile)
		if err != nil {
			return err
		}
		paramsSpec := &jobSpec{}
		for _, p := range params {
			if err := applyDirective(paramsSpec, p, "PARAMS"); err != nil {
				return err
			}
		}
		spec = mergeSpec(spec, paramsSpec)
	}
	spec = mergeSpec(spec, readNomadEnvVars())
	spec = mergeSpec(spec, scriptSpec)

	if err := applyCLIFlags(cmd, spec); err != nil {
		return err
	}
	if err := applySpecDefaults(spec, defaultName, useSBATCH); err != nil {
		return err
	}
	applyAbcNodesNomadNamespaceFromConfig(spec)

	if err := resolveAutoDriver(cmd, spec); err != nil {
		return err
	}

	var scriptBody string
	if isBuiltInHelloAbc {
		scriptBody, err = finalizeHelloAbc(spec)
		if err != nil {
			return err
		}
	} else {
		// Stamp submission metadata into meta block.
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		submissionID := newSubmissionID()
		spec.Meta["abc_submission_id"] = submissionID
		spec.Meta["abc_submission_time"] = time.Now().UTC().Format(time.RFC3339)
		if spec.Name != "" {
			base := spec.Name
			// Recognise category prefixes already supplied by callers
			// (e.g. abc data download → "data-download") so we do not
			// double-prefix with script-job-. Match either as a bare name
			// ("data-download") or as a prefix ("data-download-foo").
			hasCategoryPrefix := false
			for _, p := range []string{"script-job", "data-download", "data-upload", "data-transfer", "pipeline", "module"} {
				if base == p || strings.HasPrefix(base, p+"-") {
					hasCategoryPrefix = true
					break
				}
			}
			if !hasCategoryPrefix {
				base = "script-job-" + base
			}
			if slug := utils.ActiveWhoamiSlug(); slug != "" {
				base = slug + "-" + base
			}
			spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
		}
		scriptBody = string(scriptBytes)
		scriptBody, err = FinalizeJobScript(spec, scriptBase, scriptBody)
		if err != nil {
			return err
		}
	}

	hcl := generateHCL(spec, scriptBase, scriptBody)

	submit, _ := cmd.Flags().GetBool("submit")
	noSubmit, _ := cmd.Flags().GetBool("no-submit")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	outputFile, _ := cmd.Flags().GetString("output-file")
	if noSubmit {
		submit = false
	}
	// Keep historical generate-only behavior for --output-file unless caller
	// explicitly forces submission.
	if outputFile != "" && !cmd.Flags().Changed("submit") && !cmd.Flags().Changed("no-submit") {
		submit = false
	}

	if submit || dryRun {
		return runWithNomad(cmd.Context(), cmd, spec, hcl, submit, dryRun)
	}
	if outputFile != "" {
		return os.WriteFile(outputFile, []byte(hcl), 0644)
	}
	fmt.Fprint(cmd.OutOrStdout(), hcl)
	return nil
}

// resolveAutoDriver resolves an "auto-container" or "auto-exec" driver hint
// to a concrete Nomad driver using capabilities.nodes from the active context config.
// It mutates spec.Driver to the resolved driver and appends a placement constraint.
// If the driver is not an auto-* value, it returns immediately (no-op).
// Warnings (e.g. raw_exec fallback) are printed to stderr.
func resolveAutoDriver(cmd *cobra.Command, spec *jobSpec) error {
	if !jurist.IsAutoDriver(spec.Driver) {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config for driver resolution: %w", err)
	}
	ctx := cfg.ActiveCtx()
	if ctx.Capabilities == nil || len(ctx.Capabilities.Nodes) == 0 {
		return fmt.Errorf(
			"driver %q requires node capability data\n"+
				"  Run: abc cluster capabilities sync",
			spec.Driver,
		)
	}
	priority := ctx.JobDriverPriority()
	res, err := jurist.ResolveLocally(spec.Driver, ctx.Capabilities.Nodes, priority)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"  [jurist] %s → %s  (%s)\n",
		res.OriginalDriver, res.ResolvedDriver, res.Reason,
	)
	if res.Warning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  [jurist] Warning: %s\n", res.Warning)
	}
	spec.Driver = res.ResolvedDriver
	// Constraint: restrict placement to nodes that have the resolved driver.
	// We use a regexp on ${node.unique.id} rather than ${driver.NAME} because
	// Nomad rejects hyphenated driver names (e.g. "containerd-driver") in the
	// ${driver.X} attribute path syntax.
	if len(res.EligibleNodeIDs) > 0 {
		nodeIDPattern := "^(" + strings.Join(res.EligibleNodeIDs, "|") + ")$"
		spec.Constraints = append(spec.Constraints, nomadConstraint{
			Attribute: "${node.unique.id}",
			Operator:  "regexp",
			Value:     nodeIDPattern,
		})
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  [jurist] constraint: node.unique.id =~ %d eligible node(s)\n",
			len(res.EligibleNodeIDs),
		)
	}
	return nil
}

// runJobNativeHCL handles --format=hcl: the input file is a native Nomad HCL job
// definition. It is passed through directly without preamble parsing or HCL generation.
// Submission flags (--submit, --no-submit, --dry-run, --output-file) behave identically
// to the shell path. Most scheduler override flags (--cores, --mem, etc.) are silently
// ignored; the HCL file is the authoritative spec.
func runJobNativeHCL(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read HCL file %q: %w", path, err)
	}
	hclStr := string(data)

	submit, _ := cmd.Flags().GetBool("submit")
	noSubmit, _ := cmd.Flags().GetBool("no-submit")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	outputFile, _ := cmd.Flags().GetString("output-file")
	if noSubmit {
		submit = false
	}
	if outputFile != "" && !cmd.Flags().Changed("submit") && !cmd.Flags().Changed("no-submit") {
		submit = false
	}

	if !submit && !dryRun {
		if outputFile != "" {
			return os.WriteFile(outputFile, data, 0644)
		}
		fmt.Fprint(cmd.OutOrStdout(), hclStr)
		return nil
	}

	// Build an HCL variable-definition string from --var key=value flags.
	// Nomad's /v1/jobs/parse endpoint accepts a "Variables" field whose value
	// is an HCL string containing variable assignments (key = "value").
	var hclVars string
	if vars, _ := cmd.Flags().GetStringArray("var"); len(vars) > 0 {
		var sb strings.Builder
		for _, kv := range vars {
			idx := strings.IndexByte(kv, '=')
			if idx < 1 {
				return fmt.Errorf("--var %q: expected key=value format", kv)
			}
			key := strings.TrimSpace(kv[:idx])
			val := kv[idx+1:]
			// Quote the value as an HCL string literal.
			sb.WriteString(key + " = " + fmt.Sprintf("%q", val) + "\n")
		}
		hclVars = sb.String()
	}

	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad (%s)...\n", nomadAddrFromCmd(cmd))
	jobJSON, err := nc.ParseHCLWithVars(ctx, hclStr, hclVars)
	if err != nil {
		return fmt.Errorf("nomad HCL parse: %w", err)
	}

	jobID := extractJobIDFromJSON(jobJSON)

	if err := nc.PreflightJobTaskDrivers(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if dryRun {
		plan, err := nc.PlanJob(ctx, jobID, jobJSON)
		if err != nil {
			return fmt.Errorf("nomad plan: %w", err)
		}
		printPlan(cmd, hclStr, plan)
		return nil
	}

	fmt.Fprintln(cmd.ErrOrStderr(), "  Submitting to Nomad...")
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		return fmt.Errorf("nomad register: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  ✓ Job submitted\n")
	fmt.Fprintf(out, "  Nomad job ID   %s\n", jobID)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ⚠ Warnings: %s\n", resp.Warnings)
	}
	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", jobID)
	fmt.Fprintf(out, "    abc job show %s\n", jobID)

	if watch, _ := cmd.Flags().GetBool("watch"); watch {
		watchTimeout, _ := cmd.Flags().GetDuration("watch-timeout")
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		return watchJobLogs(ctx, nc, jobID, "", out, watchDelay, watchTimeout)
	}
	return nil
}

// extractJobIDFromJSON extracts the job ID from a Nomad job JSON blob (returned by ParseHCL).
// Falls back to an empty string if the field is absent or malformed.
func extractJobIDFromJSON(jobJSON json.RawMessage) string {
	var obj struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(jobJSON, &obj); err == nil && obj.ID != "" {
		return obj.ID
	}
	return ""
}

func runWithNomad(ctx context.Context, cmd *cobra.Command, spec *jobSpec, hcl string, submit, dryRun bool) error {
	log := debuglog.FromContext(ctx)
	nc := nomadClientFromCmd(cmd)

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad (%s)...\n", nomadAddrFromCmd(cmd))
	t := time.Now()
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "job.run.failed",
			debuglog.AttrsError("job.hcl_parse", err)...,
		)
		return fmt.Errorf("nomad HCL parse: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "job.hcl_parsed",
		slog.String("op", "job.run"),
		slog.Int("hcl_bytes", len(hcl)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	if err := nc.PreflightJobTaskDrivers(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		log.LogAttrs(ctx, debuglog.L1, "job.run.failed",
			debuglog.AttrsError("job.driver_preflight", err)...,
		)
		return err
	}

	if dryRun {
		t = time.Now()
		plan, err := nc.PlanJob(ctx, spec.Name, jobJSON)
		if err != nil {
			log.LogAttrs(ctx, debuglog.L1, "job.run.failed",
				debuglog.AttrsError("job.plan", err)...,
			)
			return fmt.Errorf("nomad plan: %w", err)
		}
		log.LogAttrs(ctx, debuglog.L1, "job.planned",
			debuglog.AttrsJobSubmit("plan", spec.Name, "", spec.Namespace, time.Since(t).Milliseconds())...,
		)
		printPlan(cmd, hcl, plan)
		return nil
	}

	region := spec.Region
	if region == "" {
		region = "default"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "  Submitting to Nomad (%s)...\n", region)
	t = time.Now()
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "job.run.failed",
			debuglog.AttrsError("job.register", err)...,
		)
		return fmt.Errorf("nomad register: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "job.submitted",
		debuglog.AttrsJobSubmit("register", spec.Name, resp.EvalID, spec.Namespace, time.Since(t).Milliseconds())...,
	)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  ✓ Job submitted\n")
	fmt.Fprintf(out, "  Nomad job ID   %s\n", spec.Name)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ⚠ Warnings: %s\n", resp.Warnings)
	}

	if watch, _ := cmd.Flags().GetBool("watch"); watch {
		watchTimeout, _ := cmd.Flags().GetDuration("watch-timeout")
		if watchTimeout <= 0 {
			watchTimeout = 0
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		return watchJobLogs(ctx, nc, spec.Name, spec.Namespace, out, watchDelay, watchTimeout)
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", spec.Name)
	fmt.Fprintf(out, "    abc job show %s\n", spec.Name)

	if notify, _ := cmd.Flags().GetBool("notify"); notify {
		printNtfySubscriptionHint(out)
	}
	return nil
}

func printPlan(cmd *cobra.Command, hcl string, plan *NomadPlanResponse) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  --- GENERATED HCL ---\n%s  ---------------------\n\n", hcl)
	fmt.Fprintf(out, "  Diff type: %s\n", plan.Diff.Type)
	for tg, upd := range plan.Annotations.DesiredTGUpdates {
		fmt.Fprintf(out, "  Task group %q: place=%d update=%d stop=%d\n",
			tg, upd.Place, upd.Update, upd.Stop)
	}
	if len(plan.FailedTGAllocs) > 0 {
		fmt.Fprintf(out, "  ⚠ Failed placements: %d task group(s) could not be placed\n",
			len(plan.FailedTGAllocs))
	}
	if plan.Warnings != "" {
		fmt.Fprintf(out, "  Warnings: %s\n", plan.Warnings)
	}
	fmt.Fprintf(out, "\n  ✓ Dry-run complete. Re-run without --dry-run to register.\n")
}

const (
	watchDelay = 10 * time.Second
)

// applyAbcNodesNomadNamespaceFromConfig sets spec.Namespace from the active abc
// context when the operator has not set --namespace or NOMAD_NAMESPACE, so
// generated HCL includes a namespace stanza and jobs land in the correct Nomad
// namespace for multi-tenant Grafana / Prometheus views.
func applyAbcNodesNomadNamespaceFromConfig(spec *jobSpec) {
	if spec == nil || strings.TrimSpace(spec.Namespace) != "" {
		return
	}
	// Bare `#ABC --namespace` / `--namespace` (no value) only exposes NOMAD_NAMESPACE
	// into the task env — do not infer scheduler namespace from the abc context.
	if spec.ExposeNamespaceEnv {
		return
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	if ns := strings.TrimSpace(cfg.ActiveCtx().AbcNodesNomadNamespaceForCLI()); ns != "" {
		spec.Namespace = ns
	}
}

func watchJobLogs(ctx context.Context, nc *nomadClient, jobID, namespace string,
	w io.Writer, delay, timeout time.Duration) error {
	return utils.WatchJobLogs(ctx, nc, jobID, namespace, w, delay, timeout)
}

func newSubmissionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sub-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

// printNtfySubscriptionHint prints the ntfy topic URL so the user can
// subscribe for job completion/failure push notifications.
func printNtfySubscriptionHint(w io.Writer) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	ctx := cfg.ActiveCtx()
	if ctx.Capabilities == nil || !ctx.Capabilities.Notifications {
		return
	}
	ntfyHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "ntfy", "http")
	if !ok || ntfyHTTP == "" {
		return
	}

	// Verify ntfy is reachable before printing (non-fatal).
	nc := floor.NewNtfyClient(ntfyHTTP)
	if !nc.Healthy(context.Background()) {
		return
	}

	topic := "abc-jobs"
	fmt.Fprintf(w, "\n  Push notifications (ntfy):\n")
	fmt.Fprintf(w, "    Subscribe: %s/%s\n", strings.TrimRight(ntfyHTTP, "/"), topic)
	fmt.Fprintf(w, "    App:       ntfy.sh  (iOS / Android / Desktop)\n")
}
