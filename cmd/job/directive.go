package job

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// stripInlineComment removes a trailing shell comment from a directive string.
// A comment begins at the first occurrence of " #" (space then hash), so
// annotated lines such as:
//
//	#ABC --cores=8    # 8 cores per task
//
// are handled correctly without treating the annotation as a directive token.
func stripInlineComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// parsePreamble reads lines from r until the first non-comment, non-blank line
// and returns directive strings from #ABC, #NOMAD, #SBATCH, and #PBS comment lines.
func parsePreamble(r io.Reader) (abcDirs, nomadDirs, slurmDirs, pbsDirs []string, err error) {
	scanner := bufio.NewScanner(r)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if first {
			first = false
			if strings.HasPrefix(trimmed, "#!") {
				continue
			}
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		switch {
		case strings.HasPrefix(trimmed, "#ABC"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#ABC"))
			rest = stripInlineComment(rest)
			if rest != "" {
				abcDirs = append(abcDirs, rest)
			}
		case strings.HasPrefix(trimmed, "#NOMAD"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#NOMAD"))
			rest = stripInlineComment(rest)
			if rest != "" {
				nomadDirs = append(nomadDirs, rest)
			}
		case strings.HasPrefix(trimmed, "#SBATCH"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#SBATCH"))
			rest = stripInlineComment(rest)
			if rest != "" {
				slurmDirs = append(slurmDirs, rest)
			}
		case strings.HasPrefix(trimmed, "#PBS"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#PBS"))
			rest = stripInlineComment(rest)
			if rest != "" {
				pbsDirs = append(pbsDirs, rest)
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, nil, nil, fmt.Errorf("error reading script: %w", scanErr)
	}
	return abcDirs, nomadDirs, slurmDirs, pbsDirs, nil
}

// applyPBSDirective parses a single #PBS directive string (short-flag style) and
// mutates spec. PBS uses single-letter flags: -q queue, -A account, -l resource=val,
// -N name, -o stdout, -e stderr, -v vars, -W extra.
func applyPBSDirective(spec *jobSpec, directive string) error {
	fields := strings.Fields(directive)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if !strings.HasPrefix(f, "-") {
			continue // bare value; skip
		}
		// Handle -flag=value and -flag value forms.
		var flag, val string
		if strings.Contains(f, "=") {
			parts := strings.SplitN(f, "=", 2)
			flag = strings.TrimPrefix(parts[0], "-")
			val = strings.Trim(parts[1], "'\"")
		} else {
			flag = strings.TrimPrefix(f, "-")
			if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "-") {
				i++
				val = strings.Trim(fields[i], "'\"")
			}
		}

		switch flag {
		case "N": // job name
			if val != "" {
				spec.Name = val
			}
		case "q": // queue → SlurmPartition (shared field for queue/partition)
			if val != "" {
				spec.SlurmPartition = val
			}
		case "A": // account
			if val != "" {
				spec.SlurmAccount = val
			}
		case "o": // stdout file
			if val != "" {
				spec.SlurmStdoutFile = val
			}
		case "e": // stderr file
			if val != "" {
				spec.SlurmStderrFile = val
			}
		case "l": // resource list: select=N:ncpus=C:mem=Mmb OR walltime=HH:MM:SS
			if val == "" {
				continue
			}
			// May be comma-separated: -l select=1:ncpus=2:mem=512mb,walltime=01:00:00
			for _, res := range strings.Split(val, ",") {
				kv := strings.SplitN(res, "=", 2)
				if len(kv) != 2 {
					continue
				}
				rkey := strings.TrimSpace(kv[0])
				rval := strings.TrimSpace(kv[1])
				switch rkey {
				case "walltime":
					secs, err := walltimeToSeconds(rval)
					if err != nil {
						return fmt.Errorf("#PBS -l walltime: %w", err)
					}
					spec.WalltimeSecs = secs
				case "select":
					// select=N:ncpus=C:mem=Mmb — parse inline key=value pairs
					for _, chunk := range strings.Split(rval, ":") {
						kv2 := strings.SplitN(chunk, "=", 2)
						if len(kv2) != 2 {
							continue
						}
						switch kv2[0] {
						case "ncpus":
							n, err := strconv.Atoi(kv2[1])
							if err == nil && n > 0 {
								spec.Cores = n
							}
						case "mem":
							mb, err := parseMemoryMB(kv2[1])
							if err == nil {
								spec.MemoryMB = mb
							}
						}
					}
				case "ncpus":
					n, err := strconv.Atoi(rval)
					if err == nil && n > 0 {
						spec.Cores = n
					}
				case "mem":
					mb, err := parseMemoryMB(rval)
					if err == nil {
						spec.MemoryMB = mb
					}
				}
			}
		case "W": // extra directives — append to SlurmExtraArgs for passthrough
			if val != "" {
				spec.SlurmExtraArgs = append(spec.SlurmExtraArgs, "-W "+val)
			}
		case "v", "V", "r", "m", "M", "j", "k", "p", "R", "S":
			// silently accepted but not mapped (mail, signal, restart, etc.)
		default:
			fmt.Fprintf(os.Stderr, "warning: unsupported #PBS directive -%s ignored\n", flag)
		}
	}
	return nil
}

// applySBATCHDirective parses a single #SBATCH directive string and mutates spec.
// Only the subset of SBATCH flags relevant to Nomad job submission is handled;
// cluster-specific flags (--nodes, --exclusive, etc.) are silently ignored.
func applySBATCHDirective(spec *jobSpec, directive string) error {
	for _, field := range strings.Fields(directive) {
		if !strings.HasPrefix(field, "--") {
			continue // skip bare values or short flags like -n
		}
		kv := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)
		key := kv[0]
		hasValue := len(kv) == 2
		val := ""
		if hasValue {
			val = strings.TrimSpace(kv[1])
			val = strings.Trim(val, "'\"")
		}

		switch key {
		case "job-name", "J":
			if hasValue && val != "" {
				spec.Name = val
			}
		case "cpus-per-task", "c":
			if hasValue {
				n, err := strconv.Atoi(val)
				if err != nil || n < 1 {
					return fmt.Errorf("#SBATCH --cpus-per-task must be a positive integer, got %q", val)
				}
				spec.Cores = n
			}
		case "ntasks", "n":
			if hasValue {
				n, err := strconv.Atoi(val)
				if err != nil || n < 1 {
					return fmt.Errorf("#SBATCH --ntasks must be a positive integer, got %q", val)
				}
				spec.SlurmNTasks = n
			}
		case "mem":
			if hasValue {
				mb, err := parseMemoryMB(val)
				if err != nil {
					return fmt.Errorf("#SBATCH --mem: %w", err)
				}
				spec.MemoryMB = mb
			}
		case "time", "t":
			if hasValue {
				secs, err := walltimeToSeconds(val)
				if err != nil {
					return fmt.Errorf("#SBATCH --time: %w", err)
				}
				spec.WalltimeSecs = secs
			}
		case "partition", "p":
			if hasValue {
				spec.SlurmPartition = val
			}
		case "account", "A":
			if hasValue {
				spec.SlurmAccount = val
			}
		case "output", "o":
			if hasValue {
				spec.SlurmStdoutFile = val
			}
		case "error", "e":
			if hasValue {
				spec.SlurmStderrFile = val
			}
		case "chdir", "D":
			if hasValue {
				spec.SlurmWorkDir = val
				spec.ChDir = val
			}
		case "qos":
			if hasValue && val != "" {
				spec.SlurmExtraArgs = append(spec.SlurmExtraArgs, "--qos="+val)
			}
		case "reservation":
			if hasValue && val != "" {
				spec.SlurmReservation = val
			}
		case "nodes", "N", "exclusive", "gres", "constraint", "licenses":
			// silently accepted; cluster-topology or unsupported
		default:
			fmt.Fprintf(os.Stderr, "warning: unsupported #SBATCH directive --%s ignored\n", key)
		}
	}
	return nil
}

// preambleMode controls which comment markers are honoured during parsing.
type preambleMode int

const (
	preambleModeAuto  preambleMode = iota // default: use #SBATCH/#PBS if present, else #ABC/#NOMAD
	preambleModeABC                       // only honour #ABC and #NOMAD; ignore #SBATCH and #PBS
	preambleModeSlurm                     // only honour #SBATCH; require at least one
	preambleModePBS                       // only honour #PBS; require at least one
)

// resolveSpec applies NOMAD then ABC directives (ABC has higher priority) and
// fills in defaults. slurmDirs contains raw #SBATCH strings, pbsDirs contains
// raw #PBS strings; mode controls which sets are active.
func resolveSpec(abcDirs, nomadDirs, slurmDirs, pbsDirs []string, defaultName string, mode preambleMode) (*jobSpec, error) {
	spec, useSBATCH, err := resolveSpecRaw(abcDirs, nomadDirs, slurmDirs, pbsDirs, mode)
	if err != nil {
		return nil, err
	}
	if err := applySpecDefaults(spec, defaultName, useSBATCH); err != nil {
		return nil, err
	}
	return spec, nil
}

func resolveSpecRaw(abcDirs, nomadDirs, slurmDirs, pbsDirs []string, mode preambleMode) (*jobSpec, bool, error) {
	spec := &jobSpec{}

	// Determine whether to honour SBATCH / PBS directives.
	useSBATCH := false
	usePBS := false
	switch mode {
	case preambleModeSlurm:
		if len(slurmDirs) == 0 {
			return nil, false, fmt.Errorf("--preamble-mode slurm requires at least one #SBATCH directive in the script")
		}
		useSBATCH = true
	case preambleModePBS:
		if len(pbsDirs) == 0 {
			return nil, false, fmt.Errorf("--preamble-mode pbs requires at least one #PBS directive in the script")
		}
		usePBS = true
	case preambleModeAuto:
		useSBATCH = len(slurmDirs) > 0
		// Only honour PBS if no SBATCH directives present (SBATCH takes precedence in auto mode).
		usePBS = !useSBATCH && len(pbsDirs) > 0
	case preambleModeABC:
		useSBATCH = false
		usePBS = false
	}

	// Apply PBS first (lowest priority; SBATCH takes precedence in auto mode).
	if usePBS {
		spec.pbsDetected = true
		for _, d := range pbsDirs {
			if err := applyPBSDirective(spec, d); err != nil {
				return nil, false, err
			}
		}
	}

	// Apply SBATCH (overrides PBS when both present, though auto mode prevents that).
	if useSBATCH {
		for _, d := range slurmDirs {
			if err := applySBATCHDirective(spec, d); err != nil {
				return nil, false, err
			}
		}
	}

	// NOMAD overrides SBATCH/PBS.
	if mode != preambleModeSlurm && mode != preambleModePBS {
		for _, d := range nomadDirs {
			if err := applyDirective(spec, d, "NOMAD"); err != nil {
				return nil, false, err
			}
		}
	}

	// ABC overrides everything.
	if mode != preambleModeSlurm {
		for _, d := range abcDirs {
			if err := applyDirective(spec, d, "ABC"); err != nil {
				return nil, false, err
			}
		}
	}

	return spec, useSBATCH, nil
}

func applySpecDefaults(spec *jobSpec, defaultName string, useSBATCH bool) error {
	if spec.Name == "" {
		spec.Name = defaultName
	}
	if spec.Nodes == 0 {
		spec.Nodes = 1
	}
	if spec.Driver == "" {
		// Auto-select driver when scheduler-specific preamble is present.
		// Caller signals PBS via a negative useSBATCH sentinel: useSBATCH==false
		// and the spec already has SlurmPartition or SlurmAccount set from PBS
		// parsing. We detect PBS via spec.pbsDetected set by resolveSpecRaw.
		if useSBATCH {
			spec.Driver = "slurm"
		} else if spec.pbsDetected {
			spec.Driver = "pbs"
		} else {
			spec.Driver = "exec"
		}
	}
	if spec.Priority == 0 {
		spec.Priority = 50
	}
	// Persist reschedule settings into meta so they survive round-trips.
	if spec.RescheduleMode != "" || spec.RescheduleAttempts != 0 ||
		spec.RescheduleInterval != "" || spec.RescheduleDelay != "" || spec.RescheduleMaxDelay != "" {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		spec.Meta["abc_reschedule_mode"] = spec.RescheduleMode
		if spec.RescheduleAttempts != 0 {
			spec.Meta["abc_reschedule_attempts"] = fmt.Sprintf("%d", spec.RescheduleAttempts)
		}
		if spec.RescheduleInterval != "" {
			spec.Meta["abc_reschedule_interval"] = spec.RescheduleInterval
		}
		if spec.RescheduleDelay != "" {
			spec.Meta["abc_reschedule_delay"] = spec.RescheduleDelay
		}
		if spec.RescheduleMaxDelay != "" {
			spec.Meta["abc_reschedule_max_delay"] = spec.RescheduleMaxDelay
		}
	}
	if spec.OutputLog != "" || spec.ErrorLog != "" || spec.Conda != "" || spec.Pixi ||
		spec.Runtime != "" || strings.TrimSpace(spec.From) != "" || spec.TaskTmp {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		if spec.OutputLog != "" {
			spec.Meta["abc_output"] = spec.OutputLog
		}
		if spec.ErrorLog != "" {
			spec.Meta["abc_error"] = spec.ErrorLog
		}
		if spec.Conda != "" {
			spec.Meta["abc_conda"] = spec.Conda
		}
		if spec.Pixi {
			spec.Meta["abc_pixi"] = "true"
		}
	}
	syncStackMetaKeys(spec)
	syncTaskTmpMeta(spec)
	if spec.NoNetwork && len(spec.Ports) > 0 {
		return fmt.Errorf("no-network cannot be combined with port mapping")
	}
	if spec.Name == "" {
		return fmt.Errorf("job name is required: set #ABC --name=<n>, #NOMAD --name=<n>, or NOMAD_JOB_NAME")
	}
	if spec.Driver != "" {
		spec.Driver = utils.NormalizeNomadTaskDriver(spec.Driver)
	}
	return nil
}

// applyDirective parses a single whitespace-separated directive string and
// mutates spec. marker is "ABC" or "NOMAD" and appears in error messages.
func applyDirective(spec *jobSpec, directive, marker string) error {
	for _, field := range strings.Fields(directive) {
		if !strings.HasPrefix(field, "--") {
			return fmt.Errorf("invalid #%s directive %q: expected --key or --key=value", marker, field)
		}
		kv := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)
		key := kv[0]
		hasValue := len(kv) == 2
		val := ""
		if hasValue {
			val = strings.TrimSpace(kv[1])
			val = strings.Trim(val, "'\"")
		}

		if strings.HasPrefix(key, "driver.config.") {
			if !hasValue || strings.TrimSpace(val) == "" {
				return fmt.Errorf("#%s --%s requires a value", marker, key)
			}
			subKey := strings.TrimPrefix(key, "driver.config.")
			// `command` and `args` would shadow the user's submitted script
			// (which abc wraps as the task command + args to preserve the
			// HPC-script UX). Reject at parse time with an explanatory
			// message rather than silently dropping at HCL-render time.
			if subKey == "command" || subKey == "args" {
				return fmt.Errorf(
					"#%s --driver.config.%s is not allowed: this would shadow the "+
						"submitted script. The script body is what runs inside the "+
						"container; reserve --driver.config for image/volumes/mounts/"+
						"network/etc. To pass arguments TO your script, write them in "+
						"the script body itself.",
					marker, subKey,
				)
			}
			if spec.DriverConfig == nil {
				spec.DriverConfig = make(map[string]string)
			}
			spec.DriverConfig[subKey] = val
			continue
		}

		switch key {
		// ── Scheduler directives ─────────────────────────────────────────────
		case "name":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --name requires a value", marker)
			}
			spec.Name = val
		case "region":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --region requires a value", marker)
			}
			spec.Region = val
		case "namespace":
			if !hasValue {
				spec.ExposeNamespaceEnv = true
			} else {
				spec.Namespace = val
			}
		case "dc":
			if !hasValue {
				spec.ExposeDCEnv = true
			} else {
				spec.Datacenters = append(spec.Datacenters, val)
			}
		case "priority":
			if !hasValue {
				return fmt.Errorf("#%s --priority requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--priority must be a positive integer, got %q", val)
			}
			spec.Priority = n
		case "nodes":
			if !hasValue {
				return fmt.Errorf("#%s --nodes requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--nodes must be a positive integer, got %q", val)
			}
			spec.Nodes = n
		case "cores":
			if !hasValue {
				return fmt.Errorf("#%s --cores requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--cores must be a positive integer, got %q", val)
			}
			spec.Cores = n
		case "mem":
			if !hasValue {
				return fmt.Errorf("#%s --mem requires a value", marker)
			}
			mb, err := parseMemoryMB(val)
			if err != nil {
				return err
			}
			spec.MemoryMB = mb
		case "gpus":
			if !hasValue {
				return fmt.Errorf("#%s --gpus requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--gpus must be a positive integer, got %q", val)
			}
			spec.GPUs = n
		case "time":
			if !hasValue {
				return fmt.Errorf("#%s --time requires a value", marker)
			}
			secs, err := walltimeToSeconds(val)
			if err != nil {
				return err
			}
			spec.WalltimeSecs = secs
		case "chdir":
			if !hasValue {
				return fmt.Errorf("#%s --chdir requires a value", marker)
			}
			spec.ChDir = val
		case "depend":
			if !hasValue {
				return fmt.Errorf("#%s --depend requires a value", marker)
			}
			spec.Depend = val
		case "driver":
			if !hasValue {
				return fmt.Errorf("#%s --driver requires a value", marker)
			}
			spec.Driver = val
		case "reschedule-mode":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-mode requires a value", marker)
			}
			spec.RescheduleMode = val
		case "reschedule-attempts":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-attempts requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return fmt.Errorf("--reschedule-attempts must be non-negative, got %q", val)
			}
			spec.RescheduleAttempts = n
		case "reschedule-interval":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-interval requires a value", marker)
			}
			spec.RescheduleInterval = val
		case "reschedule-delay":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-delay requires a value", marker)
			}
			spec.RescheduleDelay = val
		case "reschedule-max-delay":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-max-delay requires a value", marker)
			}
			spec.RescheduleMaxDelay = val
		case "output":
			if !hasValue {
				return fmt.Errorf("#%s --output requires a value", marker)
			}
			spec.OutputLog = val
		case "error":
			if !hasValue {
				return fmt.Errorf("#%s --error requires a value", marker)
			}
			spec.ErrorLog = val
		case "conda":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --conda requires a value", marker)
			}
			spec.Conda = val
		case "pixi":
			if hasValue {
				return fmt.Errorf("#%s --pixi does not accept a value", marker)
			}
			spec.Pixi = true
		case "task-tmp":
			if hasValue {
				return fmt.Errorf("#%s --task-tmp does not accept a value", marker)
			}
			spec.TaskTmp = true
		case "runtime":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --runtime requires a value (e.g. pixi-exec)", marker)
			}
			spec.Runtime = val
		case "from":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --from requires a value (path or URI to the runtime definition)", marker)
			}
			spec.From = val
		case "constraint":
			if !hasValue {
				return fmt.Errorf("#%s --constraint requires a value", marker)
			}
			c, err := parseConstraint(val)
			if err != nil {
				return err
			}
			spec.Constraints = append(spec.Constraints, c)
		case "affinity":
			if !hasValue {
				return fmt.Errorf("#%s --affinity requires a value", marker)
			}
			a, err := parseAffinity(val)
			if err != nil {
				return err
			}
			spec.Affinities = append(spec.Affinities, a)

		// ── Meta directive ───────────────────────────────────────────────────
		case "meta":
			if !hasValue {
				return fmt.Errorf("#%s --meta requires key=value format", marker)
			}
			parts := strings.SplitN(val, "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				return fmt.Errorf("#%s --meta requires key=value format, got %q", marker, val)
			}
			if spec.Meta == nil {
				spec.Meta = make(map[string]string)
			}
			spec.Meta[parts[0]] = parts[1]

		// ── Network directives ───────────────────────────────────────────────
		case "port":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --port requires a label value", marker)
			}
			if spec.NoNetwork {
				return fmt.Errorf("#%s --port cannot be used with --no-network", marker)
			}
			spec.Ports = append(spec.Ports, val)
		case "no-network":
			if hasValue {
				return fmt.Errorf("#%s --no-network does not accept a value", marker)
			}
			spec.NoNetwork = true

		// ── Runtime-exposure boolean flags ───────────────────────────────────
		case "alloc_id":
			spec.ExposeAllocID = true
		case "short_alloc_id":
			spec.ExposeShortAllocID = true
		case "alloc_name":
			spec.ExposeAllocName = true
		case "alloc_index":
			spec.ExposeAllocIndex = true
		case "job_id":
			spec.ExposeJobID = true
		case "job_name":
			spec.ExposeJobName = true
		case "parent_job_id":
			spec.ExposeParentJobID = true
		case "group_name":
			spec.ExposeGroupName = true
		case "task_name":
			spec.ExposeTaskName = true
		case "cpu_limit":
			spec.ExposeCPULimit = true
		case "cpu_cores":
			spec.ExposeCPUCores = true
		case "mem_limit":
			spec.ExposeMemLimit = true
		case "mem_max_limit":
			spec.ExposeMemMaxLimit = true
		case "alloc_dir":
			spec.ExposeAllocDir = true
		case "task_dir":
			spec.ExposeTaskDir = true
		case "secrets_dir":
			spec.ExposeSecretsDir = true
		case "hpc_compat_env":
			spec.IncludeHPCCompatEnv = true
		case "slurm-extra":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --slurm-extra requires a value", marker)
			}
			spec.SlurmExtraArgs = append(spec.SlurmExtraArgs, val)
		case "reservation":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reservation requires a value", marker)
			}
			spec.SlurmReservation = val
		case "spread":
			if hasValue {
				return fmt.Errorf("#%s --spread does not accept a value", marker)
			}
			spec.Spread = true

		default:
			return fmt.Errorf("unknown #%s directive --%s", marker, key)
		}
	}
	return nil
}

func parseConstraint(expr string) (nomadConstraint, error) {
	expr = strings.TrimSpace(expr)
	ops := []string{"==", "!=", "=~", "!~", "<=", ">=", "<", ">"}
	for _, op := range ops {
		if idx := strings.Index(expr, op); idx >= 0 {
			attr := strings.TrimSpace(expr[:idx])
			val := strings.TrimSpace(expr[idx+len(op):])
			if attr == "" || val == "" {
				return nomadConstraint{}, fmt.Errorf("invalid constraint expression %q", expr)
			}
			val = strings.Trim(val, "'\"")
			return nomadConstraint{Attribute: attr, Operator: op, Value: val}, nil
		}
	}
	return nomadConstraint{}, fmt.Errorf("invalid constraint expression %q", expr)
}

func parseAffinity(specExpr string) (nomadAffinity, error) {
	specExpr = strings.TrimSpace(specExpr)
	weight := 50
	parts := strings.Split(specExpr, ",")
	if len(parts) == 0 {
		return nomadAffinity{}, fmt.Errorf("invalid affinity expression %q", specExpr)
	}
	main := strings.TrimSpace(parts[0])
	if main == "" {
		return nomadAffinity{}, fmt.Errorf("invalid affinity expression %q", specExpr)
	}
	c, err := parseConstraint(main)
	if err != nil {
		return nomadAffinity{}, err
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(strings.TrimSpace(p), "weight=") {
			wStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "weight="))
			w, err := strconv.Atoi(wStr)
			if err != nil || w < 0 {
				return nomadAffinity{}, fmt.Errorf("invalid affinity weight %q", wStr)
			}
			weight = w
		}
	}
	return nomadAffinity{Attribute: c.Attribute, Operator: c.Operator, Value: c.Value, Weight: weight}, nil
}
