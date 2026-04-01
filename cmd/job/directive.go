package job

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
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
// and returns the directive strings from #ABC and #NOMAD comment lines.
func parsePreamble(r io.Reader) (abcDirs, nomadDirs []string, err error) {
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
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, fmt.Errorf("error reading script: %w", scanErr)
	}
	return abcDirs, nomadDirs, nil
}

// resolveSpec applies NOMAD then ABC directives (ABC has higher priority) and
// fills in defaults. The defaultName is used when no --name directive is found.
func resolveSpec(abcDirs, nomadDirs []string, defaultName string) (*jobSpec, error) {
	spec := &jobSpec{}
	for _, d := range nomadDirs {
		if err := applyDirective(spec, d, "NOMAD"); err != nil {
			return nil, err
		}
	}
	for _, d := range abcDirs {
		if err := applyDirective(spec, d, "ABC"); err != nil {
			return nil, err
		}
	}
	if spec.Name == "" {
		spec.Name = defaultName
	}
	if spec.Nodes == 0 {
		spec.Nodes = 1
	}
	if spec.Driver == "" {
		spec.Driver = "exec"
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
	if spec.OutputLog != "" || spec.ErrorLog != "" {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		if spec.OutputLog != "" {
			spec.Meta["abc_output"] = spec.OutputLog
		}
		if spec.ErrorLog != "" {
			spec.Meta["abc_error"] = spec.ErrorLog
		}
	}
	if spec.NoNetwork && len(spec.Ports) > 0 {
		return nil, fmt.Errorf("no-network cannot be combined with port mapping")
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("job name is required: set #ABC --name=<n>, #NOMAD --name=<n>, or NOMAD_JOB_NAME")
	}
	return spec, nil
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
			if spec.DriverConfig == nil {
				spec.DriverConfig = make(map[string]string)
			}
			spec.DriverConfig[strings.TrimPrefix(key, "driver.config.")] = val
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

		default:
			return fmt.Errorf("unknown #%s directive --%s", marker, key)
		}
	}
	return nil
}

func parseConstraint(expr string) (nomadConstraint, error) {
	expr = strings.TrimSpace(expr)
	ops := []string{"==", "!=", "=~", "!~", "<", "<=", ">", ">="}
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
