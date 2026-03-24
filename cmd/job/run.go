// Package job implements the "abc job" command group, including "abc job run"
// which parses preamble directives and generates a Nomad HCL batch job spec.
package job

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// jobSpec holds the configuration for a Nomad batch job.
type jobSpec struct {
	Name         string // --name
	Namespace    string // --namespace
	Nodes        int    // --nodes  (group count, default 1)
	Cores        int    // --cores
	MemoryMB     int    // --mem    (stored as MiB)
	GPUs         int    // --gpus
	WalltimeSecs int    // --time   (stored as seconds, 0 = unlimited)
	ChDir        string // --chdir
	Depend       string // --depend
	Env          map[string]string
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <script>",
		Short: "Generate a Nomad HCL batch job spec from an annotated Bash script",
		Long: `Parse preamble directives from a Bash script and print a Nomad HCL job spec.

Directive sources are consulted in priority order (highest to lowest):

  1. #ABC  preamble  — lines beginning with "#ABC"  in the script header
  2. #NOMAD preamble — lines beginning with "#NOMAD" in the script header
  3. NOMAD env vars  — NOMAD_JOB_NAME, NOMAD_NAMESPACE, NOMAD_GROUP_COUNT,
                       NOMAD_CPU_CORES, NOMAD_MEMORY_LIMIT (read at invocation time)

If a required value (job name) cannot be resolved from any source it defaults to
the script filename without extension. An error is returned if the name is still
empty after all sources are exhausted.

The script body is referenced as an artifact that Nomad fetches and executes on
the compute node via the exec2 driver.

Supported directives (identical syntax for both #ABC and #NOMAD):
  --name=<string>        Job name
  --namespace=<string>   Nomad namespace
  --nodes=<int>          Number of group instances (default: 1)
  --cores=<int>          CPU cores reserved per task
  --mem=<size>[K|M|G]    Memory per task (KiB / MiB / GiB; stored as MiB)
  --gpus=<int>           GPU count (nvidia/gpu device)
  --time=<HH:MM:SS>      Walltime limit (wrapped with the timeout command)
  --chdir=<path>         Working directory inside the task sandbox
  --depend=<type:id>     Dependency on another job (injects a prestart task)
  --env=<NOMAD_VAR>[=<value>]
                         Emit a NOMAD_* runtime environment variable. If no value
                         is provided, defaults to ${NOMAD_VAR}.

Examples:
  # Generate HCL and pipe directly to Nomad
  abc job run job.sh | nomad job run -

  # Save generated HCL to a file
  abc job run mpi_job.sh > ocean-model.nomad.hcl`,
		Args: cobra.ExactArgs(1),
		RunE: runJob,
	}
}

func runJob(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]

	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("cannot open script %q: %w", scriptPath, err)
	}
	defer f.Close()

	abcDirs, nomadDirs, err := parsePreamble(f)
	if err != nil {
		return fmt.Errorf("failed to parse script preamble: %w", err)
	}

	scriptBase := filepath.Base(scriptPath)
	defaultName := strings.TrimSuffix(scriptBase, filepath.Ext(scriptBase))

	spec, err := resolveSpec(abcDirs, nomadDirs, readNomadEnvVars(), defaultName)
	if err != nil {
		return err
	}

	fmt.Fprint(cmd.OutOrStdout(), generateHCL(spec, scriptBase))
	return nil
}

// parsePreamble scans the contiguous comment block at the top of a Bash script
// and returns separate slices of raw #ABC and #NOMAD directive strings.
// Scanning stops at the first non-comment, non-empty line after the optional shebang.
func parsePreamble(r io.Reader) (abcDirs, nomadDirs []string, err error) {
	scanner := bufio.NewScanner(r)
	first := true

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip the shebang on the very first line.
		if first {
			first = false
			if strings.HasPrefix(trimmed, "#!") {
				continue
			}
		}

		// Stop at the first non-comment, non-empty line.
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}

		switch {
		case strings.HasPrefix(trimmed, "#ABC"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#ABC"))
			if rest != "" {
				abcDirs = append(abcDirs, rest)
			}
		case strings.HasPrefix(trimmed, "#NOMAD"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#NOMAD"))
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

// readNomadEnvVars reads the standard Nomad runtime environment variables and
// returns them as a partial jobSpec. Integer env vars that fail to parse are
// silently ignored.
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

// resolveSpec builds a final jobSpec by applying sources in priority order:
//
//  1. envSpec     — values from NOMAD_* environment variables (lowest priority)
//  2. nomadDirs   — directives from #NOMAD preamble lines
//  3. abcDirs     — directives from #ABC preamble lines (highest priority)
//
// After merging, filename-based and numeric defaults are applied, then the
// result is validated. An error is returned if a required field cannot be
// resolved.
func resolveSpec(abcDirs, nomadDirs []string, envSpec *jobSpec, defaultName string) (*jobSpec, error) {
	// Start from the env-var baseline (lowest priority).
	spec := envSpec
	if spec == nil {
		spec = &jobSpec{}
	}

	// Apply #NOMAD directives (overwrite env-var values).
	for _, d := range nomadDirs {
		if err := applyDirective(spec, d, "NOMAD"); err != nil {
			return nil, err
		}
	}

	// Apply #ABC directives (highest priority; overwrite #NOMAD and env vars).
	for _, d := range abcDirs {
		if err := applyDirective(spec, d, "ABC"); err != nil {
			return nil, err
		}
	}

	// Apply defaults.
	if spec.Name == "" {
		spec.Name = defaultName
	}
	if spec.Nodes == 0 {
		spec.Nodes = 1
	}

	// Validate required fields.
	if spec.Name == "" {
		return nil, fmt.Errorf(
			"job name is required: set #ABC --name=<name>, #NOMAD --name=<name>, or NOMAD_JOB_NAME env var",
		)
	}

	return spec, nil
}

// applyDirective parses a space-separated list of --key=value tokens and
// writes the corresponding fields into spec. marker ("ABC" or "NOMAD") is used
// only in error messages.
func applyDirective(spec *jobSpec, directive, marker string) error {
	for _, field := range strings.Fields(directive) {
		if !strings.HasPrefix(field, "--") {
			return fmt.Errorf("invalid #%s directive %q: expected --key=value", marker, field)
		}
		kv := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)
		if len(kv) != 2 {
			return fmt.Errorf("invalid #%s directive %q: expected --key=value", marker, field)
		}
		key, val := kv[0], kv[1]

		switch key {
		case "name":
			spec.Name = val
		case "namespace":
			spec.Namespace = val
		case "nodes":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--nodes must be a positive integer, got %q", val)
			}
			spec.Nodes = n
		case "cores":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--cores must be a positive integer, got %q", val)
			}
			spec.Cores = n
		case "mem":
			mb, err := parseMemoryMB(val)
			if err != nil {
				return err
			}
			spec.MemoryMB = mb
		case "gpus":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--gpus must be a positive integer, got %q", val)
			}
			spec.GPUs = n
		case "time":
			secs, err := walltimeToSeconds(val)
			if err != nil {
				return err
			}
			spec.WalltimeSecs = secs
		case "chdir":
			spec.ChDir = val
		case "depend":
			spec.Depend = val
		case "env":
			if val == "" {
				return fmt.Errorf("--env must be NOMAD_* variable name, got %q", val)
			}
			parts := strings.SplitN(val, "=", 2)
			envKey := parts[0]
			envValue := ""
			hasValue := false
			if envKey == "" {
				return fmt.Errorf("--env must be NOMAD_* variable name, got %q", val)
			}
			if len(parts) == 2 {
				envValue = parts[1]
				hasValue = true
			}
			if !strings.HasPrefix(envKey, "NOMAD_") {
				return fmt.Errorf("--env only supports NOMAD_* variables, got %q", envKey)
			}
			if !hasValue {
				envValue = fmt.Sprintf("${%s}", envKey)
			}
			if spec.Env == nil {
				spec.Env = make(map[string]string)
			}
			spec.Env[envKey] = envValue
		default:
			return fmt.Errorf("unknown #%s directive --%s", marker, key)
		}
	}
	return nil
}

// parseMemoryMB converts a memory string with an optional K/M/G suffix to MiB.
// K (KiB) → ceiling to MiB, M (MiB) → as-is, G (GiB) → × 1024.
// No suffix is treated as MiB.
func parseMemoryMB(s string) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	if upper == "" {
		return 0, fmt.Errorf("empty memory value")
	}

	switch {
	case strings.HasSuffix(upper, "G"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n * 1024, nil
	case strings.HasSuffix(upper, "M"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	case strings.HasSuffix(upper, "K"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		// Convert KiB → MiB with ceiling division.
		return (n + 1023) / 1024, nil
	default:
		n, err := strconv.Atoi(upper)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	}
}

// walltimeToSeconds converts an HH:MM:SS string to a total number of seconds.
func walltimeToSeconds(t string) (int, error) {
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid --time value %q: expected HH:MM:SS", t)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	s, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	return h*3600 + m*60 + s, nil
}

// generateHCL produces a Nomad HCL job specification from the resolved spec.
func generateHCL(spec *jobSpec, scriptName string) string {
	var b strings.Builder

	// ── job block ──────────────────────────────────────────────────────────
	fmt.Fprintf(&b, "job %q {\n", spec.Name)
	fmt.Fprintf(&b, "  type = \"batch\"\n")
	if spec.Namespace != "" {
		fmt.Fprintf(&b, "  namespace = %q\n", spec.Namespace)
	}
	fmt.Fprintln(&b)

	// ── group block ────────────────────────────────────────────────────────
	fmt.Fprintf(&b, "  group \"main\" {\n")
	fmt.Fprintf(&b, "    count = %d\n", spec.Nodes)

	// ── optional prestart task for --depend ────────────────────────────────
	if spec.Depend != "" {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "    task \"wait-dependency\" {\n")
		fmt.Fprintf(&b, "      driver = \"exec2\"\n")
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "      lifecycle {\n")
		fmt.Fprintf(&b, "        hook    = \"prestart\"\n")
		fmt.Fprintf(&b, "        sidecar = false\n")
		fmt.Fprintf(&b, "      }\n")
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "      config {\n")
		fmt.Fprintf(&b, "        command = \"/bin/sh\"\n")
		fmt.Fprintf(&b, "        args    = [\"-c\", \"echo Waiting for dependency: %s\"]\n", spec.Depend)
		fmt.Fprintf(&b, "      }\n")
		fmt.Fprintf(&b, "    }\n")
	}

	// ── main task ──────────────────────────────────────────────────────────
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "    task \"main\" {\n")
	fmt.Fprintf(&b, "      driver = \"exec2\"\n")
	fmt.Fprintln(&b)

	// config block
	fmt.Fprintf(&b, "      config {\n")
	if spec.WalltimeSecs > 0 {
		fmt.Fprintf(&b, "        command  = \"timeout\"\n")
		fmt.Fprintf(&b, "        args     = [\"%d\", \"/bin/bash\", \"local/%s\"]\n", spec.WalltimeSecs, scriptName)
	} else {
		fmt.Fprintf(&b, "        command  = \"/bin/bash\"\n")
		fmt.Fprintf(&b, "        args     = [\"local/%s\"]\n", scriptName)
	}
	if spec.ChDir != "" {
		fmt.Fprintf(&b, "        work_dir = %q\n", spec.ChDir)
	}
	fmt.Fprintf(&b, "      }\n")
	fmt.Fprintln(&b)

	// artifact block
	fmt.Fprintf(&b, "      artifact {\n")
	fmt.Fprintf(&b, "        source = %q\n", scriptName)
	fmt.Fprintf(&b, "      }\n")

	// resources block (only when at least one resource is specified)
	if spec.Cores > 0 || spec.MemoryMB > 0 || spec.GPUs > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "      resources {\n")
		if spec.Cores > 0 {
			fmt.Fprintf(&b, "        cores  = %d\n", spec.Cores)
		}
		if spec.MemoryMB > 0 {
			fmt.Fprintf(&b, "        memory = %d\n", spec.MemoryMB)
		}
		if spec.GPUs > 0 {
			fmt.Fprintln(&b)
			fmt.Fprintf(&b, "        device \"nvidia/gpu\" {\n")
			fmt.Fprintf(&b, "          count = %d\n", spec.GPUs)
			fmt.Fprintf(&b, "        }\n")
		}
		fmt.Fprintf(&b, "      }\n")
	}

	// env block — always emitted with HPC→Nomad variable mappings
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "      env {\n")
	fmt.Fprintf(&b, "        SLURM_JOB_ID        = \"${NOMAD_ALLOC_ID}\"\n")
	fmt.Fprintf(&b, "        PBS_JOBID           = \"${NOMAD_ALLOC_ID}\"\n")
	fmt.Fprintf(&b, "        SLURM_JOB_NAME      = \"${NOMAD_JOB_NAME}\"\n")
	fmt.Fprintf(&b, "        PBS_JOBNAME         = \"${NOMAD_JOB_NAME}\"\n")
	fmt.Fprintf(&b, "        SLURM_SUBMIT_DIR    = \"${NOMAD_TASK_DIR}\"\n")
	fmt.Fprintf(&b, "        PBS_O_WORKDIR       = \"${NOMAD_TASK_DIR}\"\n")
	fmt.Fprintf(&b, "        SLURM_ARRAY_TASK_ID = \"${NOMAD_ALLOC_INDEX}\"\n")
	fmt.Fprintf(&b, "        PBS_ARRAYID         = \"${NOMAD_ALLOC_INDEX}\"\n")
	fmt.Fprintf(&b, "        SLURM_NTASKS        = \"${NOMAD_GROUP_COUNT}\"\n")
	fmt.Fprintf(&b, "        PBS_NP              = \"${NOMAD_GROUP_COUNT}\"\n")
	fmt.Fprintf(&b, "        SLURMD_NODENAME     = \"${NOMAD_ALLOC_HOST}\"\n")
	fmt.Fprintf(&b, "        PBS_O_HOST          = \"${NOMAD_ALLOC_HOST}\"\n")
	fmt.Fprintf(&b, "        SLURM_CPUS_ON_NODE  = \"${NOMAD_CPU_CORES}\"\n")
	fmt.Fprintf(&b, "        PBS_NUM_PPN         = \"${NOMAD_CPU_CORES}\"\n")
	fmt.Fprintf(&b, "        SLURM_MEM_PER_NODE  = \"${NOMAD_MEMORY_LIMIT}\"\n")
	fmt.Fprintf(&b, "        PBS_MEM             = \"${NOMAD_MEMORY_LIMIT}\"\n")
	if len(spec.Env) > 0 {
		fmt.Fprintln(&b)
		keys := make([]string, 0, len(spec.Env))
		for key := range spec.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "        %s = %q\n", key, spec.Env[key])
		}
	}
	fmt.Fprintf(&b, "      }\n")

	fmt.Fprintf(&b, "    }\n") // close task
	fmt.Fprintf(&b, "  }\n")   // close group
	fmt.Fprintf(&b, "}\n")     // close job

	return b.String()
}
