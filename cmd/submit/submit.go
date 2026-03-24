// Package submit implements the "abc submit" command, which parses #NOMAD
// directives from a Bash script preamble and generates a Nomad HCL job spec.
package submit

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// jobSpec holds the directives extracted from the script preamble.
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
}

// NewCmd returns the "submit" subcommand.
func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit <script>",
		Short: "Submit a Nomad batch job from an annotated Bash script",
		Long: `Parse #NOMAD directives from a Bash script preamble and print a Nomad HCL job spec.

The preamble is scanned for lines beginning with '#NOMAD'. Each such line is treated
as a job configuration directive. The script body is referenced as an artifact that
Nomad fetches and executes on the compute node via the exec2 driver.

Supported directives:
  #NOMAD --name=<string>        Job name (default: script filename without extension)
  #NOMAD --namespace=<string>   Nomad namespace
  #NOMAD --nodes=<int>          Number of group instances (default: 1)
  #NOMAD --cores=<int>          CPU cores reserved per task
  #NOMAD --mem=<size>[K|M|G]    Memory per task (KiB / MiB / GiB; stored as MiB)
  #NOMAD --gpus=<int>           GPU count (nvidia/gpu device)
  #NOMAD --time=<HH:MM:SS>      Walltime limit (wrapped with the timeout command)
  #NOMAD --chdir=<path>         Working directory inside the task sandbox
  #NOMAD --depend=<type:id>     Dependency on another job (injects a prestart task)

Examples:
  # Generate HCL from a script and pipe it to nomad
  abc submit job.sh | nomad job run -

  # Save generated HCL to a file
  abc submit mpi_job.sh > ocean-model.nomad.hcl`,
		Args: cobra.ExactArgs(1),
		RunE: runSubmit,
	}
}

func runSubmit(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]

	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("cannot open script %q: %w", scriptPath, err)
	}
	defer f.Close()

	spec, err := parsePreamble(f)
	if err != nil {
		return fmt.Errorf("failed to parse script preamble: %w", err)
	}

	// Apply defaults.
	if spec.Name == "" {
		base := filepath.Base(scriptPath)
		spec.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if spec.Nodes == 0 {
		spec.Nodes = 1
	}

	fmt.Fprint(cmd.OutOrStdout(), generateHCL(spec, filepath.Base(scriptPath)))
	return nil
}

// parsePreamble scans the contiguous comment block at the top of a Bash script
// and extracts #NOMAD directives. Scanning stops at the first non-comment,
// non-empty line after the optional shebang.
func parsePreamble(r io.Reader) (*jobSpec, error) {
	spec := &jobSpec{}
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

		// Only process lines that start with the #NOMAD marker.
		if !strings.HasPrefix(trimmed, "#NOMAD") {
			continue
		}

		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#NOMAD"))
		if rest == "" {
			continue
		}

		if err := applyDirective(spec, rest); err != nil {
			return nil, err
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading script: %w", err)
	}

	return spec, nil
}

// applyDirective parses a space-separated list of --key=value flags and
// applies them to spec.
func applyDirective(spec *jobSpec, directive string) error {
	for _, field := range strings.Fields(directive) {
		if !strings.HasPrefix(field, "--") {
			return fmt.Errorf("invalid directive %q: expected --key=value", field)
		}
		kv := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)
		if len(kv) != 2 {
			return fmt.Errorf("invalid directive %q: expected --key=value", field)
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
		default:
			return fmt.Errorf("unknown #NOMAD directive --%s", key)
		}
	}
	return nil
}

// parseMemoryMB converts a memory string with an optional K/M/G suffix to MiB.
// K (KiB) → MiB (ceiling division), M (MiB) → as-is, G (GiB) → × 1024.
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

// generateHCL produces a Nomad HCL job specification from the parsed spec.
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
	fmt.Fprintf(&b, "      }\n")

	fmt.Fprintf(&b, "    }\n") // close task
	fmt.Fprintf(&b, "  }\n")  // close group
	fmt.Fprintf(&b, "}\n")    // close job

	return b.String()
}
